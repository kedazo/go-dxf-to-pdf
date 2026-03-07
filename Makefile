APP := dxf-to-pdf
MODULE := github.com/kedazo/go-dxf-to-pdf
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_DIR := build

TARGETS := \
	linux-amd64 \
	linux-arm64 \
	windows-amd64

.PHONY: all clean release $(TARGETS)

all: release

linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(APP)-linux-amd64/$(APP) ./cmd/$(APP)/

linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(APP)-linux-arm64/$(APP) ./cmd/$(APP)/

windows-amd64:
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(APP)-windows-amd64/$(APP).exe ./cmd/$(APP)/

release: $(TARGETS)
	cd $(BUILD_DIR) && tar czf $(APP)-$(VERSION)-linux-amd64.tar.gz $(APP)-linux-amd64/
	cd $(BUILD_DIR) && tar czf $(APP)-$(VERSION)-linux-arm64.tar.gz $(APP)-linux-arm64/
	cd $(BUILD_DIR) && zip -q $(APP)-$(VERSION)-windows-amd64.zip $(APP)-windows-amd64/$(APP).exe
	@echo ""
	@echo "Release archives:"
	@ls -lh $(BUILD_DIR)/*.tar.gz $(BUILD_DIR)/*.zip

clean:
	rm -rf $(BUILD_DIR)
