.PHONY: fast check verify vendor release-acceptance

fast:
	go run ./internal/boundarygate/cmd -mode=fast

check:
	go run ./internal/boundarygate/cmd -mode=check

verify:
	go run ./internal/boundarygate/cmd -mode=verify

vendor:
	go mod vendor

# Explicitly networked; this is intentionally outside the deterministic verify gate.
release-acceptance:
	go run -mod=vendor ./internal/libraryreleaseacceptance/cmd
