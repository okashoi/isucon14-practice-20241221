package main

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
)

// このAPIをインスタンス内から一定間隔で叩かせることで、椅子とライドをマッチングさせる
func internalGetMatching(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 最も待たせているリクエスト（ride）
	ride := &Ride{}
	if err := db.GetContext(ctx, ride, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at LIMIT 1`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	type CandidateChair struct {
		ID            string `db:"id"`
		Speed         int    `db:"speed"`
		Latitude      int    `db:"latitude"`
		Longitude     int    `db:"longitude"`
		EstimatedTime float32
	}
	candidates := make([]CandidateChair, 0)

	q := `
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
	INNER JOIN (
		SELECT
			chair_id as chair_id,
			COUNT(*) = 0 as is_available
		FROM (
			SELECT
			    MAX(chair_id) as chair_id,
    	    	COUNT(chair_sent_at) = 6 AS completed
	    	FROM
    		    ride_statuses
	    	    RIGHT JOIN rides
	    	        ON ride_statuses.ride_id = rides.id
	    	WHERE
	    	    rides.chair_id IS NOT NULL
    		GROUP BY ride_id
		) ride_chair_statuses
		WHERE ride_chair_statuses.completed = FALSE
		GROUP BY chair_id
	) chair_availability
		ON chairs.id = chair_availability.chair_id
WHERE
    chairs.is_active = TRUE AND
    chair_availability.is_available = TRUE;
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

	distanceFromPickupToDestination := abs(ride.PickupLatitude-ride.DestinationLatitude) + abs(ride.PickupLongitude-ride.DestinationLongitude)
	// 配車位置までの移動時間 + 目的地までの移動時間を算出
	for _, chair := range candidates {
		distanceToPickup := abs(chair.Latitude-ride.PickupLatitude) + abs(chair.Longitude-ride.PickupLongitude)
		chair.EstimatedTime = float32(distanceToPickup+distanceFromPickupToDestination) / float32(chair.Speed)
	}

	// 移動時間が最も短いものを 1 件取得
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].EstimatedTime < candidates[j].EstimatedTime
	})

	matched := candidates[0]
	if _, err := db.ExecContext(ctx, "UPDATE rides SET chair_id = ? WHERE id = ?", matched.ID, ride.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
