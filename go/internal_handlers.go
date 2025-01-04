package main

import (
	"database/sql"
	"errors"
	"net/http"
)

// このAPIをインスタンス内から一定間隔で叩かせることで、椅子とライドをマッチングさせる
func internalGetMatching(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rides := []Ride{}
	if err := db.SelectContext(ctx, &rides, `SELECT * FROM rides WHERE chair_id IS NULL`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	type CandidateChair struct {
		ID        string `db:"id"`
		Speed     int    `db:"speed"`
		Latitude  int    `db:"latitude"`
		Longitude int    `db:"longitude"`
	}
	candidates := []CandidateChair{}

	q := `
WITH chair_statuses AS (
	SELECT
		rides.chair_id AS chair_id,
		ride_status AS status
	FROM (
		SELECT
			rides.*,
			ride_statuses.status AS ride_status,
			ROW_NUMBER() OVER (PARTITION BY chair_id ORDER BY ride_statuses.created_at DESC) AS rn
		FROM
			rides
			INNER JOIN ride_statuses
				ON rides.id = ride_statuses.ride_id AND ride_statuses.chair_sent_at IS NOT NULL
	) r
	WHERE
		r.rn = 1
)
SELECT
	chairs.id as id,
	chair_models.speed as speed,
	latest_chair_locations.latitude as latitude,
	latest_chair_locations.longitude as longitude
FROM
    chairs
	INNER JOIN chair_models
        ON chairs.model = chair_models.name
	INNER JOIN latest_chair_locations
		ON chairs.id = latest_chair_locations.chair_id
	LEFT JOIN chair_statuses
		ON chairs.id = chair_statuses.chair_id
WHERE
    chairs.is_active = TRUE AND
    chair_statuses.status IS NULL OR chair_statuses.status = 'COMPLETED';
`

	if err := db.SelectContext(ctx, &candidates, q); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(candidates) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// distanceFromPickupToDestination := abs(ride.PickupLatitude-ride.DestinationLatitude) + abs(ride.PickupLongitude-ride.DestinationLongitude)
	// 配車位置までの移動時間を算出
	for _, ride := range rides {
		var minChairIdx int
		var minChair *CandidateChair
		var minEstimatedTime float32
		for i, chair := range candidates {
			distanceToPickup := abs(chair.Latitude-ride.PickupLatitude) + abs(chair.Longitude-ride.PickupLongitude)
			estimatedTime := float32(distanceToPickup) / float32(chair.Speed)
			if minChair == nil || estimatedTime < minEstimatedTime {
				minChairIdx = i
				minChair = &chair
				minEstimatedTime = estimatedTime
			}
		}
		if minChair != nil {
			if _, err := db.ExecContext(ctx, "UPDATE rides SET chair_id = ? WHERE id = ?", candidates[minChairIdx].ID, ride.ID); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
		}

		candidates = append(candidates[:minChairIdx], candidates[minChairIdx+1:]...)
	}

	w.WriteHeader(http.StatusNoContent)
}
