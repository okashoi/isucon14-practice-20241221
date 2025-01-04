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
		c.id AS chair_id,
		lrs.status AS status,
	FROM
		chairs c
	LEFT JOIN
		rides r ON c.id = r.chair_id
	LEFT JOIN
		latest_ride_statuses lrs ON r.id = lrs.ride_id
	WHERE
		lrs.created_at = (
			SELECT
				MAX(sub_lrs.created_at)
			FROM
				latest_ride_statuses sub_lrs
			WHERE
				sub_lrs.ride_id = r.id
		)
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

	for _, ride := range rides {
		var minChairIdx int
		var minChair *CandidateChair
		var minEstimatedTime float32
		// distanceFromPickupToDestination := abs(ride.PickupLatitude-ride.DestinationLatitude) + abs(ride.PickupLongitude-ride.DestinationLongitude)
		for i, chair := range candidates {
			// 配車位置までの移動時間を算出
			distanceToPickup := abs(chair.Latitude-ride.PickupLatitude) + abs(chair.Longitude-ride.PickupLongitude)
			estimatedTime := float32(distanceToPickup) / float32(chair.Speed)
			if minChair == nil || estimatedTime < minEstimatedTime {
				minChairIdx = i
				minChair = &chair
				minEstimatedTime = estimatedTime
			}
		}
		if minChair != nil {
			if _, err := db.ExecContext(ctx, "UPDATE rides SET chair_id = ? WHERE id = ?", minChair.ID, ride.ID); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}

			candidates = append(candidates[:minChairIdx], candidates[minChairIdx+1:]...)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
