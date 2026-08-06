.PHONY: fast check benchmark verify vendor release-acceptance

fast:
	go run ./internal/boundarygate/cmd -mode=fast

check:
	go run ./internal/boundarygate/cmd -mode=check

benchmark:
	go run ./internal/boundarygate/cmd -mode=benchmark

verify:
	go run ./internal/boundarygate/cmd -mode=verify

vendor:
	go mod vendor

# Explicitly networked; this is intentionally outside the deterministic verify gate.
release-acceptance:
	go run -mod=vendor ./internal/libraryreleaseacceptance/cmd
