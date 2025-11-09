LDFLAGS += -X main.version=$$(git describe --always --abbrev=40 --dirty)
PKG_NAME=kubectl
TERRAFORM_PLUGINS=$(HOME)/.terraform.d/plugins
GOPATH?=$$(go env GOPATH)

default: build

build:
	go build -ldflags "${LDFLAGS}"

install: build
	mkdir -p ${TERRAFORM_PLUGINS}
	mv terraform-provider-kubectl ${TERRAFORM_PLUGINS}
	go install -ldflags "${LDFLAGS}"

fmt:
	gofmt -s -d -e ./kubectl

lint: $(GOPATH)/bin/golangci-lint
	GOLANGCI_LINT_CACHE=/tmp/golangci-lint-cache/ $(GOPATH)/bin/golangci-lint run kubectl

$(GOPATH)/bin/golangci-lint:
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(GOPATH)/bin v1.62.2

test:
	go test -v ./kubectl/...

testacc:
	TF_ACC=1 go test -v ./kubectl/... -timeout 120m -count=1

vet:
	@echo "go vet ."
	@go vet $$(go list ./... | grep -v vendor/) ; if [ $$? -eq 1 ]; then \
		echo ""; \
		echo "Vet found suspicious constructs. Please check the reported constructs"; \
		echo "and fix them if necessary before submitting the code for review."; \
		exit 1; \
	fi

clean:
	rm -f terraform-provider-kubectl

.PHONY: build install test testacc fmt lint vet clean
