//go:build tools

// Package deps pins every library of plan §2.1 (Phase 0 and Phase 1) so that
// `go mod tidy` never drops one before a work package imports it. Work packages
// never edit go.mod/go.sum; a new dependency is a contract request (plan §0.6).
package deps

import (
	_ "github.com/RoaringBitmap/roaring/v2"
	_ "github.com/SherClockHolmes/webpush-go"
	_ "github.com/gen2brain/avif"
	_ "github.com/gen2brain/webp"
	_ "github.com/getkin/kin-openapi/openapi3"
	_ "github.com/getkin/kin-openapi/openapi3filter"
	_ "github.com/go-chi/chi/v5"
	_ "github.com/go-webauthn/webauthn/protocol"
	_ "github.com/go-webauthn/webauthn/webauthn"
	_ "github.com/google/go-cmp/cmp"
	_ "github.com/google/uuid"
	_ "github.com/gowebpki/jcs"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/kaptinlin/messageformat-go"
	_ "github.com/kaptinlin/messageformat-go/mf1"
	_ "github.com/minio/minio-go/v7"
	_ "github.com/tink-crypto/tink-go/v2/daead/subtle"
	_ "github.com/yuin/goldmark"
	_ "github.com/yuin/goldmark/extension"
	_ "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	_ "golang.org/x/net/html"
	_ "golang.org/x/net/idna"
	_ "golang.org/x/net/publicsuffix"
	_ "golang.org/x/text/language"
	_ "golang.org/x/text/secure/precis"
	_ "golang.org/x/text/unicode/norm"
	_ "golang.org/x/tools/go/packages"
	_ "pgregory.net/rapid"
)
