package main

import (
	"context"
	"crypto/subtle"
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

type ctxKey string

const claimsKey ctxKey = "claims"

type Claims struct {
	UserID int    `json:"user_id"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
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

func generateHashFromPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("generate has error: %v", err)
	}

	return string(hash), nil
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

func checkRobotToken(r *http.Request, robotID string) bool {
	got, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found {
		return false
	}

	want := "robot_token_" + robotID
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
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
