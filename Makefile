REGISTRY_NAME?=docker.io/iejalapeno
IMAGE_VERSION?=latest

.PHONY: all linkstate-edge-v6 container push clean test

ifdef V
TESTARGS = -v -args -alsologtostderr -v 5
else
TESTARGS =
endif

all: linkstate-edge-v6

linkstate-edge-v6:
	mkdir -p bin
	$(MAKE) -C ./cmd compile-linkstate-edge-v6

linkstate-edge-v6-container: linkstate-edge-v6
	docker build -t $(REGISTRY_NAME)/linkstate-edge-v6:$(IMAGE_VERSION) -f ./build/Dockerfile.linkstate-edge-v6 .

push: linkstate-edge-v6-container
	docker push $(REGISTRY_NAME)/linkstate-edge-v6:$(IMAGE_VERSION)

clean:
	rm -rf bin

test:
	GO111MODULE=on go test `go list ./... | grep -v 'vendor'` $(TESTARGS)
	GO111MODULE=on go vet `go list ./... | grep -v vendor`
