package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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
	Timestamp      string       `json:"timestamp"`
	Location       Location     `json:"location"`
	ScanResults    []ScanResult `json:"scan_results"`
	BatteryLevel   int          `json:"battery_level"`
	NextCheckpoint string       `json:"next_checkpoint"`
}

type RobotScanOut struct {
	ID           string       `json:"robot_id"`
	Timestamp    string       `json:"timestamp"`
	Location     Location     `json:"location"`
	ScanResults  []ScanResult `json:"scan_results"`
	BatteryLevel int          `json:"battery_level"`
	Status       string       `json:"status"`
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
			fmt.Fprintf(os.Stderr, "json error in login: %v", err)
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
			fmt.Fprintf(os.Stderr, "login query failed: %v", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		token, err := generateToken(user, []byte(jwtSecret))
		if err != nil {
			fmt.Fprintf(os.Stderr, "token generation failed: %v", err)
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

func robotsDataHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RobotRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			fmt.Printf("json decode error: %v", err)
			http.Error(w, "error json data", http.StatusBadRequest)
		}
		t, err := time.Parse(time.RFC3339, req.Timestamp)
		if err != nil {
			t = time.Now() //TODO: придумать что делать, если формат времени не совпадает с нужным
		}

		ctx := r.Context()
		robotStatus := ""
		if len(req.ScanResults) > 0 {
			robotStatus = "active"
		} else {
			robotStatus = "off"
		}

		_, err = pool.Exec(ctx, `
		INSERT INTO robots (id, status, battery_level, last_update, current_zone, current_row, current_shelf) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO NOTHING;`, req.ID, robotStatus, req.BatteryLevel, t, req.Location.Zone, req.Location.Row, req.Location.Shelf)

		if err != nil {
			fmt.Fprintf(os.Stderr, "robot insert error: %v\n", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		for _, s := range req.ScanResults {
			_, err = pool.Exec(ctx, `INSERT INTO products (id, name) VALUES ($1, $2)
			ON CONFLICT (id) DO NOTHING`, s.ID, s.Name)

			if err != nil {
				fmt.Fprintf(os.Stderr, "product insert error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}

			_, err := pool.Exec(ctx, `
			INSERT INTO inventory_history 
			(robot_id, product_id, quantity, zone, row_number, shelf_number, status, scanned_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				req.ID, s.ID, s.Quantity, req.Location.Zone, req.Location.Row, req.Location.Shelf, s.Status, t)

			if err != nil {
				fmt.Fprintf(os.Stderr, "iventory_history insert error: %v\n", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "received"})
	}
}

func dashboardCurrentHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := pool.Query(r.Context(), `SELECT h.robot_id, h.product_id, p.name, h.quantity, h.zone, COALESCE(h.status, ''), h.scanned_at
		FROM inventory_history h JOIN products p ON p.id = h.product_id ORDER BY h.scanned_at DESC LIMIT 20`)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dashboard select error: %v", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		scans := []ScanOut{}
		for rows.Next() {
			var s ScanOut
			if err := rows.Scan(
				&s.RobotID,
				&s.ProductID,
				&s.Name,
				&s.Quantity,
				&s.Zone,
				&s.Status,
				&s.ScannedAt); err != nil {
				fmt.Fprintf(os.Stderr, "scan error: %v", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
			scans = append(scans, s)
		}

		robots := []RobotScanOut{}
		rows, err = pool.Query(r.Context(), `SELECT * FROM robots`) //TODO: надо перечислить нужные колонки явно
		if err != nil {
			fmt.Fprintf(os.Stderr, "dashboard select error: %v", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var robot RobotScanOut
			var timeStamp time.Time
			if err := rows.Scan(
				&robot.ID,
				&robot.Status, //TODO: прочекать актуальность статусов
				&robot.BatteryLevel,
				&timeStamp,
				&robot.Location.Zone,
				&robot.Location.Row,
				&robot.Location.Shelf,
			); err != nil {
				fmt.Fprintf(os.Stderr, "scan error: %v", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
			robot.Timestamp = timeStamp.String()
			robots = append(robots, robot)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"robots":       robots,
			"recent_scans": scans,
			"statistics": map[string]int{ //TODO: расширить статистики
				"robot_count": len(robots),
				"scans_count": len(scans),
			}})
	}
}

func newUser(ctx context.Context, pool *pgxpool.Pool) error {
	name := "ivan"
	email := "ivan@example.com"
	password_hash, err := generateHashFromPassword("12345")
	if err != nil {
		return fmt.Errorf("generate hash for user: %v", err)
	}
	role := "dev"

	if _, err = pool.Exec(ctx, `INSERT INTO users (email, password_hash, name, role) VALUES ($1, $2, $3, $4)`, email, password_hash, name, role); err != nil {
		return fmt.Errorf("insert new user into db: %v", err)
	}

	return nil
}

func main() {

	ctx := context.Background()

	jwtSecret := os.Getenv("JWT_SECRET")
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

	http.HandleFunc("POST /api/robots/data", robotsDataHandler(pool))
	http.Handle("GET /api/dashboard/current",
		authMiddleware([]byte(jwtSecret), http.HandlerFunc(dashboardCurrentHandler(pool))))
	http.HandleFunc("POST /api/auth/login", loginHandler(pool, jwtSecret))

	http.ListenAndServe("localhost:3030", nil)
}
