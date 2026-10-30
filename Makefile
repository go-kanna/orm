# Use this repository's go.work even when GOWORK points at the go-kanna-wide one.
export GOWORK := $(CURDIR)/go.work

BUILD_DIR = bin

# The databases make example migrates, describes, and runs the example against, each generated for in a module of its
# own.
EXAMPLE_POSTGRES ?= postgres://kanna:kanna@127.0.0.1:15432/kanna_orm?sslmode=disable
# MySQL is named by a URL to describe, and by a DSN of go-sql-driver/mysql to migrate and run on.
EXAMPLE_MYSQL ?= mysql://kanna:kanna@127.0.0.1:13306/kanna_orm
EXAMPLE_MYSQL_DSN ?= kanna:kanna@tcp(127.0.0.1:13306)/kanna_orm?parseTime=true&multiStatements=true

# The database make bench fills and runs the benchmarks against, in a schema of their own, and the flags of their go
# test: -count=6, say, for benchstat.
BENCH_POSTGRES ?= postgres://kanna:kanna@127.0.0.1:15432/kanna_orm?sslmode=disable&search_path=bench
BENCH_FLAGS ?= -benchmem

.PHONY: test
test:
	go test ./... -race -coverprofile=coverage.out -covermode=atomic

.PHONY: test-db
test-db:
	KANNA_ORM_TEST_POSTGRES='postgres://kanna:kanna@127.0.0.1:15432/kanna_orm?sslmode=disable' \
	KANNA_ORM_TEST_MYSQL='root:kanna@tcp(127.0.0.1:13306)/?multiStatements=true' \
		$(MAKE) test

.PHONY: db-up
db-up:
	docker compose up -d --wait

.PHONY: db-down
db-down:
	docker compose down

.PHONY: example
example:
	cd example/postgres && go run ./migrate '$(EXAMPLE_POSTGRES)' && \
		go tool kanna-orm describe -database-url '$(EXAMPLE_POSTGRES)' ./... && \
		go generate ./... && go vet ./... && go run . '$(EXAMPLE_POSTGRES)'
	cd example/mysql && go run ./migrate '$(EXAMPLE_MYSQL_DSN)' && \
		go tool kanna-orm describe -database-url '$(EXAMPLE_MYSQL)' ./... && \
		go generate ./... && go vet ./... && go run . '$(EXAMPLE_MYSQL_DSN)'

.PHONY: bench
bench:
	cd bench/postgres && go run ./migrate '$(BENCH_POSTGRES)' && \
		go tool kanna-orm describe -database-url '$(BENCH_POSTGRES)' ./kanna/... && \
		go generate ./kanna/... && go vet ./... && \
		KANNA_ORM_BENCH_POSTGRES='$(BENCH_POSTGRES)' go test -count=1 -run '^TestSame' . && \
		KANNA_ORM_BENCH_POSTGRES='$(BENCH_POSTGRES)' go test -run '^$$' -bench . $(BENCH_FLAGS) .

# bench-gen regenerates the code of sqlc and ent, which make bench leaves as it is: sqlc is built from source, with cgo.
.PHONY: bench-gen
bench-gen:
	cd bench/postgres && go generate ./sqlc ./ent/db

.PHONY: lint
lint:
	@for dir in $$(go list -m -f '{{.Dir}}'); do \
		echo ">>> lint $$dir"; \
		(cd $$dir && golangci-lint run) || exit 1; \
	done

.PHONY: lint-fix
lint-fix:
	@for dir in $$(go list -m -f '{{.Dir}}'); do \
		echo ">>> lint-fix $$dir"; \
		(cd $$dir && golangci-lint run --fix) || exit 1; \
	done

.PHONY: build
build:
	go build -o $(BUILD_DIR)/kanna-orm ./cmd/kanna-orm

.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)
