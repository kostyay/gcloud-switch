.PHONY: build test fmt vet lint install release

build:
	go build -o gctx ./cmd/gctx

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

lint: fmt vet test

install:
	go install ./cmd/gctx

release: lint
	@git fetch --tags origin && \
	if [ -n "$(VERSION)" ]; then TAG=$(VERSION); \
	else LATEST=$$(git tag -l 'v*' --sort=-v:refname | sed -n '1p'); \
	  if [ -z "$$LATEST" ]; then TAG=v0.1.0; \
	  else V=$${LATEST#v}; P=$${V##*.}; TAG=v$${V%.*}.$$((P+1)); fi; \
	fi && \
	if [ "$(CONFIRM)" = "y" ]; then ans=y; else printf "Release $$TAG? [y/N] " && read ans; fi && [ "$$ans" = y ] && \
	git tag "$$TAG" && git push origin "$$TAG" && \
	GITHUB_TOKEN=$$(gh auth token) HOMEBREW_TAP_TOKEN=$$(gh auth token) goreleaser release --clean
