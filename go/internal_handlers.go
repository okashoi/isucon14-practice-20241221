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

	for {
		// 最も待たせているリクエスト（ride）を取得
		ride := &Ride{}
		if err := db.GetContext(ctx, ride, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at LIMIT 1`); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// 未割り当てのリクエストがなくなった場合
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		// 候補となる椅子を取得
		type CandidateChair struct {
			ID                   string `db:"id"`
			Speed                int    `db:"speed"`
			CurrentLatitude      int    `db:"latitude"`
			CurrentLongitude     int    `db:"longitude"`
			DestinationLatitude  int    `db:"destination_latitude"`
			DestinationLongitude int    `db:"destination_longitude"`
			EstimatedTime        float32
		}
		candidates := make([]CandidateChair, 0)

		q := `
SELECT
	chairs.id AS id,
	chair_models.speed AS speed,
	latest_chair_locations.latitude AS latitude,
	latest_chair_locations.longitude AS longitude,
	COALESCE(rides.destination_latitude, latest_chair_locations.latitude) AS destination_latitude,
	COALESCE(rides.destination_longitude, latest_chair_locations.longitude) AS destination_longitude
FROM
	chairs
	INNER JOIN chair_models
		ON chairs.model = chair_models.name
	INNER JOIN latest_chair_locations
		ON chairs.id = latest_chair_locations.chair_id
	LEFT JOIN rides
		ON chairs.id = rides.chair_id
	LEFT JOIN latest_ride_statuses
		ON rides.id = latest_ride_statuses.ride_id
WHERE
	chairs.is_active = TRUE
	AND (latest_ride_statuses.status IS NULL OR latest_ride_statuses.status = 'COMPLETED')
`
		if err := db.SelectContext(ctx, &candidates, q); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// 利用可能な椅子がない場合
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if len(candidates) == 0 {
			// 利用可能な椅子がない場合
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// 移動時間を算出（現在の目的地から次の乗車位置まで）
		for i, chair := range candidates {
			// 現在の目的地から次の乗車位置までの距離
			distanceToPickup := abs(chair.DestinationLatitude-ride.PickupLatitude) + abs(chair.DestinationLongitude-ride.PickupLongitude)
			candidates[i].EstimatedTime = float32(distanceToPickup) / float32(chair.Speed)
		}

		// 移動時間が最も短いものを選択
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].EstimatedTime < candidates[j].EstimatedTime
		})

		var matched CandidateChair
		found := false
		for _, candidate := range candidates {
			// 適切な椅子が見つかった場合に選択
			matched = candidate
			found = true
			break
		}

		if !found {
			// マッチングできる椅子がない場合
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// ライドに椅子を割り当て
		if _, err := db.ExecContext(ctx, "UPDATE rides SET chair_id = ? WHERE id = ?", matched.ID, ride.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
}
