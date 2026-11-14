package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type Location struct {
	Zone  string `json:"zone"`
	Row   int    `json:"row"`
	Shelf int    `json:"shelf"`
}

type ScanResult struct {
	ID       string `json:"product_id"`
	Name     string `json:"product_name"`
	Quantity int    `json:"quantity"`
	Status   string `json:"status"`
}

type RobotRequest struct {
	ID             string       `json:"robot_id"`
	LastUpdate     time.Time    `json:"timestamp"`
	Location       Location     `json:"location"`
	ScanResults    []ScanResult `json:"scan_results"`
	BatteryLevel   float32      `json:"battery_level"`
	NextCheckpoint string       `json:"next_checkpoint"`
}

type RobotDashboard struct {
	ID           string    `json:"robot_id"`
	LastUpdate   time.Time `json:"last_update"`
	Location     Location  `json:"location"`
	BatteryLevel float32   `json:"battery_level"`
	Status       string    `json:"status"`
}

type ScanOut struct {
	RobotID   string    `json:"robot_id"`
	ProductID string    `json:"product_id"`
	Name      string    `json:"product_name"`
	Quantity  int       `json:"quantity"`
	Zone      string    `json:"zone"`
	Status    string    `json:"status"`
	ScannedAt time.Time `json:"scanned_at"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type UserResponse struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type ctxKey string

const claimsKey ctxKey = "claims"

type Claims struct {
	UserID int    `json:"user_id"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

func authMiddleware(secret []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !found || tokenStr == "" {
			http.Error(w, "missing or invalid authorization header", http.StatusUnauthorized)
			return
		}

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims,
			func(t *jwt.Token) (any, error) { return secret, nil },
			jwt.WithValidMethods([]string{"HS256"}),
		)
		if err != nil || !token.Valid {
			fmt.Fprintf(os.Stderr, "invalid or expired token: %v\n", err)
			http.Error(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func generateToken(user UserResponse, secret []byte) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: user.ID,
		Role:   user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

func loginHandler(pool *pgxpool.Pool, jwtSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req LoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			fmt.Fprintf(os.Stderr, "json error in login: %v\n", err)
			http.Error(w, "invaild json", http.StatusBadRequest)
			return
		}

		if req.Email == "" || req.Password == "" { //TODO: придумать проверку покруче
			http.Error(w, "email and password are required", http.StatusBadRequest)
			return
		}

		var user UserResponse
		var hash string
		err := pool.QueryRow(r.Context(),
			`SELECT id, name, role, password_hash FROM users WHERE email = $1`,
			req.Email).Scan(&user.ID, &user.Name, &user.Role, &hash)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "invalid credentials", http.StatusUnauthorized)
				return
			}
			fmt.Fprintf(os.Stderr, "login query failed: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}

		token, err := generateToken(user, []byte(jwtSecret))
		if err != nil {
			fmt.Fprintf(os.Stderr, "token generation failed: %v\n", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"token": token, "user": user})
	}
}

func generateHashFromPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("generate has error: %v", err)
	}

	return string(hash), nil
}

func checkRobotStatus(last_update time.Time, battery_level float32) string {
	if time.Since(last_update) > 60*time.Second {
		return "offline"
	}
	if battery_level < 20 {
		return "low_battery"
	}
	return "active"
}

func validateRobotRequest(req RobotRequest) error {
	if strings.TrimSpace(req.ID) == "" {
		return errors.New("id is required")
	}

	if req.BatteryLevel > 100 || req.BatteryLevel < 0 {
		return errors.New("invalid battery level")
	}

	if strings.TrimSpace(req.Location.Zone) == "" {
		return errors.New("zone is required")
	}

	for _, s := range req.ScanResults {
		if strings.TrimSpace(s.ID) == "" {
			return errors.New("product id is required")
		}

		if s.Quantity < 0 {
			return errors.New("quantity must be >= 0")
		}
	}

	return nil
}

func checkRobotToken(r *http.Request, robotID string) bool {
	got, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found {
		return false
	}

	want := "robot_token_" + robotID
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

func robotsDataHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

		var req RobotRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			fmt.Fprintf(os.Stderr, "json decode error: %v\n", err)
			http.Error(w, "error json data", http.StatusBadRequest)
			return
		}

		if !checkRobotToken(r, req.ID) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if err := validateRobotRequest(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.LastUpdate.IsZero() {
			req.LastUpdate = time.Now()
		}
		req.LastUpdate = req.LastUpdate.UTC()

		robotStatus := checkRobotStatus(time.Now().UTC(), req.BatteryLevel)

		ctx := r.Context()
		tx, err := pool.Begin(r.Context())
		if err != nil {
			fmt.Fprintf(os.Stderr, "create transaction error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback(r.Context())

		_, err = tx.Exec(ctx, `
			INSERT INTO robots (id, status, battery_level, last_update, current_zone, current_row, current_shelf)
			VALUES ($1, $2, $3, NOW(), $4, $5, $6)
			ON CONFLICT (id) DO UPDATE SET
				status = EXCLUDED.status,
				battery_level = EXCLUDED.battery_level,
				last_update = EXCLUDED.last_update,
				current_zone = EXCLUDED.current_zone,
				current_row = EXCLUDED.current_row,
				current_shelf = EXCLUDED.current_shelf`,
			req.ID, robotStatus, req.BatteryLevel, req.Location.Zone, req.Location.Row, req.Location.Shelf)

		if err != nil {
			fmt.Fprintf(os.Stderr, "robot insert error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		for _, s := range req.ScanResults {
			_, err = tx.Exec(ctx, `INSERT INTO products (id, name) VALUES ($1, $2)
			ON CONFLICT (id) DO NOTHING`, s.ID, s.Name)

			if err != nil {
				fmt.Fprintf(os.Stderr, "product insert error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}

			_, err := tx.Exec(ctx, `
			INSERT INTO inventory_history 
			(robot_id, product_id, quantity, zone, row_number, shelf_number, status, scanned_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				req.ID, s.ID, s.Quantity, req.Location.Zone, req.Location.Row, req.Location.Shelf, s.Status, req.LastUpdate)

			if err != nil {
				fmt.Fprintf(os.Stderr, "inventory_history insert error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
		}

		if err := tx.Commit(r.Context()); err != nil {
			fmt.Fprintf(os.Stderr, "commit transaction error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":     "received",
			"message_id": fmt.Sprintf("%s-%d", req.ID, time.Now().UnixNano()),
		})
	}
}

func dashboardCurrentHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scanRows, err := pool.Query(r.Context(), `SELECT h.robot_id, h.product_id, p.name, h.quantity, h.zone, COALESCE(h.status, ''), h.scanned_at
		FROM inventory_history h JOIN products p ON p.id = h.product_id ORDER BY h.scanned_at DESC LIMIT 20`)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dashboard select error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		defer scanRows.Close()

		scans := []ScanOut{}
		for scanRows.Next() {
			var s ScanOut
			if err := scanRows.Scan(
				&s.RobotID,
				&s.ProductID,
				&s.Name,
				&s.Quantity,
				&s.Zone,
				&s.Status,
				&s.ScannedAt); err != nil {
				fmt.Fprintf(os.Stderr, "current scan error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
			scans = append(scans, s)
		}
		if err := scanRows.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "interating scans error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		robots := []RobotDashboard{}
		robotRows, err := pool.Query(r.Context(),
			`SELECT id, battery_level, last_update, current_zone, current_row, current_shelf 
			FROM robots`)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dashboard select error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		defer robotRows.Close()

		for robotRows.Next() {
			var robot RobotDashboard
			if err := robotRows.Scan(
				&robot.ID,
				&robot.BatteryLevel,
				&robot.LastUpdate,
				&robot.Location.Zone,
				&robot.Location.Row,
				&robot.Location.Shelf,
			); err != nil {
				fmt.Fprintf(os.Stderr, "robot scan error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
			robot.Status = checkRobotStatus(robot.LastUpdate, robot.BatteryLevel)
			robots = append(robots, robot)
		}
		if err := robotRows.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "interating robots error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		var scansToday int
		if err = pool.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM inventory_history WHERE scanned_at >= CURRENT_DATE`,
		).Scan(&scansToday); err != nil {
			fmt.Fprintf(os.Stderr, "count scans error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"robots":       robots,
			"recent_scans": scans,
			"statistics": map[string]int{ //TODO: расширить статистики
				"robot_count": len(robots),
				"scans_count": scansToday,
			}})
	}
}

type HistoryFilter struct {
	Zones    []string
	Statuses []string
	From     *time.Time
	To       *time.Time
}

func (f *HistoryFilter) where() (string, []any) {
	conds := []string{}
	args := []any{}

	if len(f.Zones) > 0 {
		args = append(args, f.Zones)
		conds = append(conds, fmt.Sprintf("h.zone = ANY($%d)", len(args)))
	}

	if len(f.Statuses) > 0 {
		args = append(args, f.Statuses)
		conds = append(conds, fmt.Sprintf("h.status = ANY($%d)", len(args)))
	}

	if f.From != nil {
		args = append(args, *f.From)
		conds = append(conds, fmt.Sprintf("h.scanned_at >= $%d", len(args)))
	}

	if f.To != nil {
		args = append(args, f.To.AddDate(0, 0, 1))
		conds = append(conds, fmt.Sprintf("h.scanned_at < $%d", len(args)))
	}

	if len(conds) == 0 {
		return "", args
	}

	return " WHERE " + strings.Join(conds, " AND "), args
}

func checkFilterZone(zone string) bool {
	if zone >= "A" && zone <= "Z" {
		return true
	}

	return false
}

var validStatuses = map[string]bool{
	"OK":        true,
	"LOW_STOCK": true,
	"CRITICAL":  true,
}

func checkFilterStatus(status string) bool {
	return validStatuses[status]
}

func parseHistoryFilters(r *http.Request) (HistoryFilter, error) {
	var f HistoryFilter
	q := r.URL.Query()

	if s := q.Get("from"); s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return HistoryFilter{}, errors.New("invalid 'from' parameter")
		}
		f.From = &t
	}

	if s := q.Get("to"); s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return HistoryFilter{}, errors.New("invalid 'to' parameter")
		}
		f.To = &t
	}

	if f.From != nil && f.To != nil && f.From.After(*f.To) {
		return HistoryFilter{}, errors.New("from must be <= to")
	}

	if s := q.Get("zone"); s != "" {
		for _, z := range strings.Split(s, ",") {
			z = strings.ToUpper(strings.TrimSpace(z))
			if !checkFilterZone(z) {
				return HistoryFilter{}, errors.New("invalid zone")
			}
			f.Zones = append(f.Zones, z)
		}
	}

	if s := q.Get("status"); s != "" {
		for _, status := range strings.Split(s, ",") {
			status = strings.ToUpper(strings.TrimSpace(status))
			if !checkFilterStatus(status) {
				return HistoryFilter{}, errors.New("invalid status")
			}
			f.Statuses = append(f.Statuses, status)
		}
	}

	return f, nil
}

func parsePagination(r *http.Request) (limit, offset int, err error) {
	limit, offset = 50, 0

	if s := r.URL.Query().Get("limit"); s != "" {
		limit, err = strconv.Atoi(s)
		if err != nil || limit < 1 || limit > 200 {
			return 0, 0, errors.New("limit must be 1...200")
		}
	}

	if s := r.URL.Query().Get("offset"); s != "" {
		offset, err = strconv.Atoi(s)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("offset must be >= 0")
		}
	}

	return limit, offset, nil
}

func historyHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset, err := parsePagination(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		filter, err := parseHistoryFilters(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		where, args := filter.where()

		itemsQuery := fmt.Sprintf(`SELECT h.robot_id, h.product_id, p.name, h.quantity, h.zone, COALESCE(h.status, ''), h.scanned_at
		FROM inventory_history h JOIN products p ON p.id = h.product_id %s ORDER BY h.scanned_at DESC, h.id DESC LIMIT %d OFFSET %d`, where, limit, offset)

		historyRows, err := pool.Query(r.Context(), itemsQuery, args...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "history query: %v", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		defer historyRows.Close()

		items := []ScanOut{}
		for historyRows.Next() {
			var s ScanOut
			if err := historyRows.Scan(
				&s.RobotID,
				&s.ProductID,
				&s.Name,
				&s.Quantity,
				&s.Zone,
				&s.Status,
				&s.ScannedAt); err != nil {
				fmt.Fprintf(os.Stderr, "history history scan error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
			items = append(items, s)
		}
		if err := historyRows.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "interating history items error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		totalCountQuery := `SELECT COUNT(*) FROM inventory_history h JOIN products p ON p.id = h.product_id ` + where

		var total int
		if err := pool.QueryRow(r.Context(), totalCountQuery, args...).Scan(&total); err != nil {
			fmt.Fprintf(os.Stderr, "count filtered items error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"total": total,
			"items": items,
			"pagination": map[string]int{
				"limit":  limit,
				"offset": offset,
			},
		})
	}
}

func newUser(ctx context.Context, pool *pgxpool.Pool) error {
	name := "admin"
	email := "admin@example.com"
	password_hash, err := generateHashFromPassword("admin12345")
	if err != nil {
		return fmt.Errorf("generate hash for user: %v\n", err)
	}
	role := "admin"

	if _, err = pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, name, role) 
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (email) DO NOTHING;`, email, password_hash, name, role); err != nil {
		return fmt.Errorf("insert new user into db: %v\n", err)
	}

	return nil
}

func main() {

	ctx := context.Background()

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		fmt.Fprint(os.Stderr, "JWT_SECRET is required")
		os.Exit(1)
	}

	connString := fmt.Sprintf("postgres://%v:%v@localhost:5432/%v", os.Getenv("POSTGRES_USER"), os.Getenv("POSTGRES_PASSWORD"), os.Getenv("POSTGRES_DB"))
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect databse: %v\n", err)
		os.Exit(1)
	}

	if err := pool.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Ping database error: %v\n", err)
		os.Exit(1)
	} else {
		fmt.Println("database connected successfully")
	}

	if err = newUser(ctx, pool); err != nil {
		fmt.Fprintf(os.Stderr, "create new user: %v\n", err)
	} else {
		fmt.Println("user created successfully")
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello, world!")
	})

	// Исторические данные
	http.Handle("GET /api/inventory/history",
		authMiddleware([]byte(jwtSecret), http.HandlerFunc(historyHandler(pool))))
	// Получение данных от робота
	http.HandleFunc("POST /api/robots/data", robotsDataHandler(pool))
	// Получение текущего состояния
	http.Handle("GET /api/dashboard/current",
		authMiddleware([]byte(jwtSecret), http.HandlerFunc(dashboardCurrentHandler(pool))))
	// Авторизация
	http.HandleFunc("POST /api/auth/login", loginHandler(pool, jwtSecret))

	http.ListenAndServe("localhost:3030", nil)
}
