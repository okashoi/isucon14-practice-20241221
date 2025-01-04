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

	tx := db.MustBegin()
	defer tx.Rollback()
	q := `
SELECT
	c.id AS id,
	cm.speed AS speed,
	ifnull(lcl.latitude, 0) AS latitude,
	ifnull(lcl.longitude, 0) AS longitude
FROM
	chairs c
	INNER JOIN chair_models cm
		ON c.model = cm.name
	LEFT JOIN latest_chair_locations lcl
		ON c.id = lcl.chair_id
WHERE
	c.is_active = TRUE
    AND ((SELECT COUNT(chair_sent_at) FROM ride_statuses rs INNER JOIN rides as r ON r.id = rs.ride_id WHERE r.chair_id = c.id) % 6 = 0)
    AND ((SELECT COUNT(1) FROM ride_statuses rs INNER JOIN rides as r ON r.id = rs.ride_id WHERE r.chair_id = c.id AND rs.status != 'COMPLETED') = 0)
FOR UPDATE SKIP LOCKED
`

	if err := tx.SelectContext(ctx, &candidates, q); err != nil {
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
			if _, err := tx.ExecContext(ctx, "UPDATE rides SET chair_id = ? WHERE id = ?", minChair.ID, ride.ID); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}

			candidates = append(candidates[:minChairIdx], candidates[minChairIdx+1:]...)
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
