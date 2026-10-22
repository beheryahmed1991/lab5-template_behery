package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	jwtmiddleware "github.com/auth0/go-jwt-middleware/v3"
	"github.com/auth0/go-jwt-middleware/v3/jwks"
	"github.com/auth0/go-jwt-middleware/v3/validator"
)

// authConfig contains public verification settings, not a client secret.
type authConfig struct {
	Issuer   string
	Audience string
	JWKSURL  string
}

type requestIdentityKey struct{}

// Only the JWT middleware creates this value from validated claims.
type requestIdentity struct {
	Subject       string
	Authorization string
}

func identityFromContext(ctx context.Context) requestIdentity {
	identity, _ := ctx.Value(requestIdentityKey{}).(requestIdentity)
	return identity
}

func authConfigFromEnv() authConfig {
	return authConfig{
		Issuer:   os.Getenv("AUTH0_ISSUER"),
		Audience: os.Getenv("AUTH0_AUDIENCE"),
		JWKSURL:  os.Getenv("AUTH0_JWKS_URL"),
	}
}

// The production entry point must wrap routing before accepting requests.
func newAuthenticatedGatewayHandler(reservationURL, loyaltyURL, paymentURL string) (http.Handler, error) {
	protect, err := newAuthMiddleware(authConfigFromEnv())
	if err != nil {
		return nil, fmt.Errorf("configure gateway authentication: %w", err)
	}
	handler, err := newGatewayHandler(reservationURL, loyaltyURL, paymentURL)
	if err != nil {
		return nil, err
	}
	gateway := handler.(*gatewayHandler)
	gateway.Handler = protect(gateway.Handler)
	return gateway, nil
}

func newAuthMiddleware(config authConfig) (func(http.Handler) http.Handler, error) {
	issuer, err := authEndpoint("AUTH0_ISSUER", config.Issuer)
	if err != nil {
		return nil, err
	}
	keysURL, err := authEndpoint("AUTH0_JWKS_URL", config.JWKSURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.Audience) == "" {
		return nil, fmt.Errorf("AUTH0_AUDIENCE is required")
	}
	provider, err := jwks.NewCachingProvider(
		jwks.WithIssuerURL(issuer),
		jwks.WithCustomJWKSURI(keysURL),
		jwks.WithCustomClient(&http.Client{Timeout: 5 * time.Second}),
		jwks.WithCacheTTL(5*time.Minute),
	)
	if err != nil {
		return nil, fmt.Errorf("configure JWKS: %w", err)
	}
	tokenValidator, err := validator.New(
		validator.WithKeyFunc(provider.KeyFunc),
		validator.WithAlgorithm(validator.RS256),
		validator.WithIssuer(config.Issuer),
		validator.WithAudience(config.Audience),
		validator.WithRegisteredClaimsValidator(func(claims validator.RegisteredClaims) error {
			if claims.Expiry == 0 || strings.TrimSpace(claims.Subject) == "" {
				return fmt.Errorf("exp and sub are required")
			}
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("configure JWT validation: %w", err)
	}
	middleware, err := jwtmiddleware.New(
		jwtmiddleware.WithValidator(tokenValidator),
		jwtmiddleware.WithValidateOnOptions(true),
		jwtmiddleware.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			gatewayError(w, http.StatusUnauthorized, "Unauthorized")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("configure authentication: %w", err)
	}
	return func(next http.Handler) http.Handler {
		secured := middleware.CheckJWT(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, err := jwtmiddleware.GetClaims[*validator.ValidatedClaims](r.Context())
			if err != nil {
				gatewayError(w, http.StatusUnauthorized, "Unauthorized")
				return
			}
			identity := requestIdentity{
				Subject:       claims.RegisteredClaims.Subject,
				Authorization: r.Header.Get("Authorization"),
			}
			r = r.Clone(context.WithValue(r.Context(), requestIdentityKey{}, identity))

			r.Header.Del("X-User-Name")
			next.ServeHTTP(w, r)
		}))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if (path != "/api" && !strings.HasPrefix(path, "/api/")) ||
				path == "/api/v1/authorize" || path == "/api/v1/callback" {
				next.ServeHTTP(w, r)
				return
			}
			secured.ServeHTTP(w, r)
		})
	}, nil
}

// HTTPS is required outside loopback, where local JWKS test servers run.
func authEndpoint(name, value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%s must be an absolute HTTPS URL", name)
	}
	loopback := parsed.Hostname() == "localhost" ||
		parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1"
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
		return nil, fmt.Errorf("%s must use HTTPS", name)
	}
	return parsed, nil
}
