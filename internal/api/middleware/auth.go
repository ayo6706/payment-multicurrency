package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ayo6706/payment-multicurrency/internal/api/problem"
	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const (
	userContextKey  contextKey = "user_id"
	roleContextKey  contextKey = "user_role"
	traceContextKey contextKey = "trace_id"
)

var jwtSecret []byte
var jwtIssuer string
var jwtAudience string

type authClaims struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

func SetJWTSecret(secret string) {
	if secret == "" {
		return
	}
	jwtSecret = []byte(secret)
}

func SetJWTValidation(issuer, audience string) {
	jwtIssuer = strings.TrimSpace(issuer)
	jwtAudience = strings.TrimSpace(audience)
}

func JWTSecret() []byte {
	clone := make([]byte, len(jwtSecret))
	copy(clone, jwtSecret)
	return clone
}

func JWTIssuer() string {
	return jwtIssuer
}

func JWTAudience() string {
	return jwtAudience
}

// AuthMiddleware validates the JWT token and injects user metadata into the context.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			problem.Write(w, r, http.StatusUnauthorized, problem.Type("auth/authorization-header-required"), http.StatusText(http.StatusUnauthorized), "Authorization header required")
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			problem.Write(w, r, http.StatusUnauthorized, problem.Type("auth/invalid-token-format"), http.StatusText(http.StatusUnauthorized), "Invalid token format")
			return
		}
		if len(jwtSecret) == 0 {
			problem.Write(w, r, http.StatusInternalServerError, problem.Type("auth/misconfigured"), http.StatusText(http.StatusInternalServerError), "auth is not configured")
			return
		}

		claims := &authClaims{}
		opts := []jwt.ParserOption{jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()})}
		if jwtIssuer != "" {
			opts = append(opts, jwt.WithIssuer(jwtIssuer))
		}
		if jwtAudience != "" {
			opts = append(opts, jwt.WithAudience(jwtAudience))
		}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, fmt.Errorf("unexpected signing method: %s", token.Method.Alg())
			}
			return jwtSecret, nil
		}, opts...)
		if err != nil || !token.Valid {
			problem.Write(w, r, http.StatusUnauthorized, problem.Type("auth/invalid-token"), http.StatusText(http.StatusUnauthorized), "Invalid token")
			return
		}
		if claims.UserID == "" {
			problem.Write(w, r, http.StatusUnauthorized, problem.Type("auth/invalid-token-claims"), http.StatusText(http.StatusUnauthorized), "Invalid token claims")
			return
		}
		if claims.Subject != "" && claims.Subject != claims.UserID {
			problem.Write(w, r, http.StatusUnauthorized, problem.Type("auth/invalid-token-claims"), http.StatusText(http.StatusUnauthorized), "Invalid token claims")
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, claims.UserID)
		ctx = context.WithValue(ctx, roleContextKey, claims.Role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole ensures the authenticated user has the required role.
func RequireRole(requiredRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := UserRoleFromContext(r.Context())
			if role != requiredRole {
				problem.Write(w, r, http.StatusForbidden, problem.Type("auth/insufficient-permissions"), http.StatusText(http.StatusForbidden), "insufficient permissions")
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
