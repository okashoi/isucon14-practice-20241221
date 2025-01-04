package main

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
)

func internalGetMatching(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. 待機中のライドを取得
	rides := []Ride{}
	if err := db.SelectContext(ctx, &rides, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(rides) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 2. 利用可能な椅子を取得
	type Chair struct {
		ID        string `db:"id"`
		Speed     int    `db:"speed"`
		Latitude  int    `db:"latitude"`
		Longitude int    `db:"longitude"`
	}
	chairs := []Chair{}
	q := `
SELECT
	chairs.id as id,
	chair_models.speed as speed,
	latest_chair_locations.latitude as latitude,
	latest_chair_locations.longitude as longitude
FROM
	chairs
	INNER JOIN chair_models ON chairs.model = chair_models.name
	INNER JOIN latest_chair_locations ON chairs.id = latest_chair_locations.chair_id
WHERE
	chairs.is_active = TRUE
`
	if err := db.SelectContext(ctx, &chairs, q); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(chairs) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 3. 二部グラフマッチングのためのコスト行列を作成
	type Match struct {
		RideID  string `db:"ride_id"`
		ChairID string
		Cost    float64
	}
	matches := []Match{}
	for _, ride := range rides {
		for _, chair := range chairs {
			// コストを計算（移動時間）
			distanceToPickup := abs(chair.Latitude-ride.PickupLatitude) + abs(chair.Longitude-ride.PickupLongitude)
			estimatedTime := float64(distanceToPickup) / float64(chair.Speed)
			matches = append(matches, Match{
				RideID:  ride.ID,
				ChairID: chair.ID,
				Cost:    estimatedTime,
			})
		}
	}

	// 4. コストを基に二部マッチングを解く
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Cost < matches[j].Cost
	})

	assignedRides := map[string]string{} // RideID -> ChairID
	assignedChairs := map[string]bool{}

	for _, match := range matches {
		if _, rideAssigned := assignedRides[match.RideID]; rideAssigned {
			continue
		}
		if _, chairAssigned := assignedChairs[match.ChairID]; chairAssigned {
			continue
		}
		assignedRides[match.RideID] = match.ChairID
		assignedChairs[match.ChairID] = true
	}

	// 5. マッチング結果をデータベースに保存
	for rideID, chairID := range assignedRides {
		if _, err := db.ExecContext(ctx, "UPDATE rides SET chair_id = ? WHERE id = ?", chairID, rideID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
