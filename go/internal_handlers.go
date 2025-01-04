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
SELECT
	c.id AS id,
	cm.speed AS speed,
	lcl.latitude AS latitude,
	lcl.longitude AS longitude
FROM
	chairs c
	INNER JOIN chair_models cm
		ON c.model = cm.name
	LEFT JOIN latest_chair_locations lcl
		ON c.id = lcl.chair_id
	LEFT JOIN (
		SELECT
			r.chair_id,
			lrs.status
		FROM
			rides r
		LEFT JOIN latest_ride_statuses lrs
				ON r.id = lrs.ride_id AND lrs.chair_sent_at IS NOT NULL
		WHERE
			r.updated_at = (
				SELECT MAX(sub_r.updated_at)
				FROM rides sub_r
				WHERE sub_r.chair_id = r.chair_id
			)
	) lr
		ON c.id = lr.chair_id
WHERE
	c.is_active = TRUE AND
	(lr.status = 'COMPLETED' OR lr.status IS NULL)
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
