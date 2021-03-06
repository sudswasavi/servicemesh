GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOGET=$(GOCMD) get
BINARY_NAME=servicemesh
REL_PATH=github.com/avinetworks/servicemesh

.PHONY:all
all: build docker

.PHONY: build
build: 
		$(GOBUILD) -o $(BINARY_NAME) $(REL_PATH)

.PHONY: clean
clean: 
		$(GOCLEAN)
		rm -f $(BINARY_NAME)

.PHONY: deps
deps:
	dep ensure -v

.PHONY: docker
docker:
	docker build -t $(BINARY_NAME):latest -f Dockerfile .
