# Use this module alone even when GOWORK points at the go-kanna-wide workspace.
export GOWORK := off

.PHONY: test
test:
	go test ./... -race -coverprofile=coverage.out -covermode=atomic

.PHONY: test-db
test-db:
	KANNA_ORM_TEST_POSTGRES='postgres://kanna:kanna@127.0.0.1:15432/kanna_orm?sslmode=disable' $(MAKE) test

.PHONY: db-up
db-up:
	docker compose up -d --wait

.PHONY: db-down
db-down:
	docker compose down

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
