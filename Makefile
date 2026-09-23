sources := $(wildcard lib/*.go) $(wildcard lib/*.c) $(wildcard lib/*.h) $(wildcard log/*.go) $(wildcard main/*.go) $(wildcard niri/*.go) $(wildcard module/*.go)
overview-sources := $(wildcard cmd/niri-overview/*.go) $(wildcard overview/*.go) $(wildcard log/*.go) $(wildcard niri/*.go)

waybar-niri-windows.so: $(sources)
	go build -buildmode=c-shared -o $@ ./main

waybar-niri-windows-debug.so: $(sources)
	go build -buildmode=c-shared -tags debug -o $@ ./main

# Standalone all-workspaces minimap renderer (lock screen background).
niri-overview: $(overview-sources)
	go build -trimpath -ldflags="-s -w" -o $@ ./cmd/niri-overview

waybar:
	waybar -c test/config.jsonc -s test/style.css

clean:
	rm -f waybar-niri-windows.so
	rm -f waybar-niri-windows-debug.so
	rm -f niri-overview

.PHONY: waybar clean niri-overview
