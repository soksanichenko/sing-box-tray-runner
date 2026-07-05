.PHONY: build clean

BUILD_DIR := build
OUTPUT    := $(BUILD_DIR)/sing_box_tray_runner.exe

build:
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
		-ldflags="-H windowsgui -s -w" \
		-o $(OUTPUT) \
		.
	@echo "Built: $(OUTPUT)"

clean:
	rm -rf $(BUILD_DIR)
