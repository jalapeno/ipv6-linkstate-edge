REGISTRY_NAME?=docker.io/iejalapeno
IMAGE_VERSION?=latest

.PHONY: all ipv6-linkstate-edge container push clean test

ifdef V
TESTARGS = -v -args -alsologtostderr -v 5
else
TESTARGS =
endif

all: ipv6-linkstate-edge

ipv6-linkstate-edge:
	mkdir -p bin
	$(MAKE) -C ./cmd compile-ipv6-linkstate-edge

ipv6-linkstate-edge-container: ipv6-linkstate-edge
	docker build -t $(REGISTRY_NAME)/ipv6-linkstate-edge:$(IMAGE_VERSION) -f ./build/Dockerfile.ipv6-linkstate-edge .

push: ipv6-linkstate-edge-container
	docker push $(REGISTRY_NAME)/ipv6-linkstate-edge:$(IMAGE_VERSION)

clean:
	rm -rf bin

test:
	GO111MODULE=on go test `go list ./... | grep -v 'vendor'` $(TESTARGS)
	GO111MODULE=on go vet `go list ./... | grep -v vendor`
