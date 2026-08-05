.PHONY: fast check verify vendor

fast:
	go run ./internal/boundarygate/cmd -mode=fast

check:
	go run ./internal/boundarygate/cmd -mode=check

verify:
	go run ./internal/boundarygate/cmd -mode=verify

vendor:
	go mod vendor
