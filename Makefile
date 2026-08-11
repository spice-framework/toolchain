.PHONY: fast check benchmark tools-bootstrap verify verify-release verify-release-artifacts vendor release-acceptance

fast:
	go run ./internal/boundarygate/cmd -mode=fast

check:
	go run ./internal/boundarygate/cmd -mode=check

benchmark:
	go run ./internal/boundarygate/cmd -mode=benchmark

verify:
	go run ./internal/boundarygate/cmd -mode=verify

tools-bootstrap:
	go run ./internal/boundarygate/cmd -mode=tools-bootstrap

verify-release:
	go run ./internal/boundarygate/cmd -mode=verify-release

verify-release-artifacts:
	go run ./internal/boundarygate/cmd -mode=release-artifacts -artifacts="$(SPICE_DISTRIBUTION_VERIFIED_ARTIFACT_DIR)"

vendor:
	go mod vendor

# Explicitly networked; this is intentionally outside the deterministic verify gate.
release-acceptance:
	go run -mod=vendor ./internal/libraryreleaseacceptance/cmd
