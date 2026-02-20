package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const (
	userContextKey  contextKey = "user_id"
	roleContextKey  contextKey = "user_role"
	traceContextKey contextKey = "trace_id"
)

var jwtSecret = []byte("change-me")

func SetJWTSecret(secret string) {
	if secret == "" {
		return
	}
	jwtSecret = []byte(secret)
}

func JWTSecret() []byte {
	return jwtSecret
}

// AuthMiddleware validates the JWT token and injects user metadata into the context.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			http.Error(w, "Invalid token format", http.StatusUnauthorized)
			return
		}

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, http.ErrAbortHandler
			}
			return jwtSecret, nil
		})
		if err != nil || !token.Valid {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			http.Error(w, "Invalid token claims", http.StatusUnauthorized)
			return
		}

		userID, _ := claims["user_id"].(string)
		role, _ := claims["role"].(string)
		if userID == "" {
			http.Error(w, "Invalid user_id in token", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, userID)
		ctx = context.WithValue(ctx, roleContextKey, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole ensures the authenticated user has the required role.
func RequireRole(requiredRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := UserRoleFromContext(r.Context())
			if role != requiredRole {
				http.Error(w, "insufficient permissions", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// UserIDFromContext returns the authenticated user ID.
func UserIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(userContextKey).(string); ok {
		return v
	}
	return ""
}

// UserRoleFromContext returns the role of the authenticated user.
func UserRoleFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(roleContextKey).(string); ok {
		return v
	}
	return ""
}

// TraceIDFromContext returns the trace id for the request.
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(traceContextKey).(string); ok {
		return v
	}
	return ""
}
