package main

import (
	crand "crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/felixge/fgprof"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"
)

var db *sqlx.DB

func main() {
	http.DefaultServeMux.Handle("/debug/fgprof", fgprof.Handler())
	go func() {
		log.Println(http.ListenAndServe(":6060", nil))
	}()

	mux := setup()
	slog.Info("Listening on :8080")
	http.ListenAndServe(":8080", mux)
}

func setup() http.Handler {
	host := os.Getenv("ISUCON_DB_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("ISUCON_DB_PORT")
	if port == "" {
		port = "3306"
	}
	_, err := strconv.Atoi(port)
	if err != nil {
		panic(fmt.Sprintf("failed to convert DB port number from ISUCON_DB_PORT environment variable into int: %v", err))
	}
	user := os.Getenv("ISUCON_DB_USER")
	if user == "" {
		user = "isucon"
	}
	password := os.Getenv("ISUCON_DB_PASSWORD")
	if password == "" {
		password = "isucon"
	}
	dbname := os.Getenv("ISUCON_DB_NAME")
	if dbname == "" {
		dbname = "isuride"
	}

	dbConfig := mysql.NewConfig()
	dbConfig.User = user
	dbConfig.Passwd = password
	dbConfig.Addr = net.JoinHostPort(host, port)
	dbConfig.Net = "tcp"
	dbConfig.DBName = dbname
	dbConfig.ParseTime = true
	dbConfig.InterpolateParams = true

	_db, err := sqlx.Connect("mysql", dbConfig.FormatDSN())
	if err != nil {
		panic(err)
	}
	db = _db

	go func() {
		lastProcessedID := ""
		for {
			newLastProcessedID, err := processBatchUpsertLatestLocation(db, lastProcessedID)
			if err != nil {
				log.Printf("Error in batch processing: %v", err)
			}

			// 更新された最新のIDを保存
			if newLastProcessedID != "" {
				lastProcessedID = newLastProcessedID
			}

			// 1秒スリープ
			time.Sleep(1 * time.Second)
		}
	}()

	mux := chi.NewRouter()
	mux.Use(middleware.Logger)
	mux.Use(middleware.Recoverer)
	mux.HandleFunc("POST /api/initialize", postInitialize)

	// app handlers
	{
		mux.HandleFunc("POST /api/app/users", appPostUsers)

		authedMux := mux.With(appAuthMiddleware)
		authedMux.HandleFunc("POST /api/app/payment-methods", appPostPaymentMethods)
		authedMux.HandleFunc("GET /api/app/rides", appGetRides)
		authedMux.HandleFunc("POST /api/app/rides", appPostRides)
		authedMux.HandleFunc("POST /api/app/rides/estimated-fare", appPostRidesEstimatedFare)
		authedMux.HandleFunc("POST /api/app/rides/{ride_id}/evaluation", appPostRideEvaluatation)
		authedMux.HandleFunc("GET /api/app/notification", appGetNotification)
		authedMux.HandleFunc("GET /api/app/nearby-chairs", appGetNearbyChairs)
	}

	// owner handlers
	{
		mux.HandleFunc("POST /api/owner/owners", ownerPostOwners)

		authedMux := mux.With(ownerAuthMiddleware)
		authedMux.HandleFunc("GET /api/owner/sales", ownerGetSales)
		authedMux.HandleFunc("GET /api/owner/chairs", ownerGetChairs)
	}

	// chair handlers
	{
		mux.HandleFunc("POST /api/chair/chairs", chairPostChairs)

		authedMux := mux.With(chairAuthMiddleware)
		authedMux.HandleFunc("POST /api/chair/activity", chairPostActivity)
		authedMux.HandleFunc("POST /api/chair/coordinate", chairPostCoordinate)
		authedMux.HandleFunc("GET /api/chair/notification", chairGetNotification)
		authedMux.HandleFunc("POST /api/chair/rides/{ride_id}/status", chairPostRideStatus)
	}

	// internal handlers
	{
		mux.HandleFunc("GET /api/internal/matching", internalGetMatching)
	}

	return mux
}

type postInitializeRequest struct {
	PaymentServer string `json:"payment_server"`
}

type postInitializeResponse struct {
	Language string `json:"language"`
}

func postInitialize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req := &postInitializeRequest{}
	if err := bindJSON(r, req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if out, err := exec.Command("../sql/init.sh").CombinedOutput(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to initialize: %s: %w", string(out), err))
		return
	}

	if _, err := db.ExecContext(ctx, "UPDATE settings SET value = ? WHERE name = 'payment_gateway_url'", req.PaymentServer); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, postInitializeResponse{Language: "go"})

}

type Coordinate struct {
	Latitude  int `json:"latitude"`
	Longitude int `json:"longitude"`
}

func bindJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func writeJSON(w http.ResponseWriter, statusCode int, v interface{}) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	buf, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(statusCode)
	w.Write(buf)
}

func writeError(w http.ResponseWriter, statusCode int, err error) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(statusCode)
	buf, marshalError := json.Marshal(map[string]string{"message": err.Error()})
	if marshalError != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"marshaling error failed"}`))
		return
	}
	w.Write(buf)

	slog.Error("error response wrote", err)
}

func secureRandomStr(b int) string {
	k := make([]byte, b)
	if _, err := crand.Read(k); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", k)
}

// バッチ処理を関数化
func processBatchUpsertLatestLocation(db *sqlx.DB, lastProcessedID string) (string, error) {
	// chair_locationsから新しいデータを取得
	rows, err := fetchNewLocations(db, lastProcessedID)
	if err != nil {
		return "", fmt.Errorf("failed to fetch new locations: %w", err)
	}
	defer rows.Close()

	// 最新のIDを格納
	var latestID string
	dataProcessed := false

	for rows.Next() {
		var id, chairID string
		var latitude, longitude float64
		var createdAt string

		if err := rows.Scan(&id, &chairID, &latitude, &longitude, &createdAt); err != nil {
			log.Printf("Failed to scan row: %v", err)
			continue
		}

		// UPSERTでデータを更新
		if err := upsertLatestLocation(db, chairID, latitude, longitude); err != nil {
			log.Printf("Failed to upsert data for chair_id=%s: %v", chairID, err)
			continue
		}

		// 最新のIDを記録
		latestID = id
		dataProcessed = true
	}

	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("error while iterating rows: %w", err)
	}

	// データが処理されていない場合、IDを空にして終了
	if !dataProcessed {
		return "", nil
	}

	return latestID, nil
}

// chair_locationsから前回処理したID以降のデータを取得
func fetchNewLocations(db *sqlx.DB, lastProcessedID string) (*sql.Rows, error) {
	query := `
		SELECT id, chair_id, latitude, longitude, created_at
		FROM chair_locations
		WHERE id > ?
		ORDER BY created_at
	`
	return db.Query(query, lastProcessedID)
}

// 最新の位置と総移動距離をUPSERT
func upsertLatestLocation(db *sqlx.DB, chairID string, newLat, newLong float64) error {
	// UPSERTクエリを実行
	upsertQuery := `
		INSERT INTO latest_chair_locations (chair_id, latitude, longitude, total_distance, created_at)
		VALUES (?, ?, ?, 0, NOW())
		ON DUPLICATE KEY UPDATE
			total_distance = total_distance +
				IF(latitude IS NOT NULL AND longitude IS NOT NULL,
					ABS(latitude - VALUES(latitude)) + ABS(longitude - VALUES(longitude)),
					0
				),
			latitude = VALUES(latitude),
			longitude = VALUES(longitude),
			created_at = VALUES(created_at)
	`
	_, err := db.Exec(upsertQuery, chairID, newLat, newLong)
	if err != nil {
		return fmt.Errorf("failed to execute upsert: %w", err)
	}

	return nil
}
