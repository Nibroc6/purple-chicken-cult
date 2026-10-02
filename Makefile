# Thin wrapper over scripts/*.sh (POSIX bash; works under Git Bash on Windows).
.PHONY: dev test test-go test-web lint build gen e2e check check-wp migrate db-reset

dev:
	bash scripts/dev.sh

gen:
	bash scripts/gen.sh

test: test-go test-web

test-go:
	bash scripts/test-go.sh

test-web:
	npm --prefix web test

lint:
	bash scripts/lint.sh

build:
	bash scripts/build.sh

e2e:
	bash scripts/e2e.sh

check:
	bash scripts/check.sh

# make check-wp WP=p0-w05
check-wp:
	bash scripts/check-wp.sh $(WP)

migrate:
	go run ./cmd/pcc migrate

db-reset:
	bash scripts/db-reset.sh
