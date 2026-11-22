package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

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

	connString := fmt.Sprintf("postgres://%v:%v@%v:5432/%v", os.Getenv("POSTGRES_USER"), os.Getenv("POSTGRES_PASSWORD"), os.Getenv("POSTGRES_HOST"), os.Getenv("POSTGRES_DB"))
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
	// Загрузка CSV
	http.Handle("POST /api/inventory/import",
		authMiddleware([]byte(jwtSecret), http.HandlerFunc(importHandler(pool))))
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

	http.ListenAndServe(":3030", nil)
}
