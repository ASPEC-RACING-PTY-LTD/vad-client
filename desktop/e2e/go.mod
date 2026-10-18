// Test-only module: end-to-end tests of the desktop engine against a real
// vad-server. It needs a sibling checkout at ../../../vad-server and is never
// built into the shipped client.
module github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/e2e

go 1.26.0

replace (
	github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop => ../
	github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk => ../../sdk
	github.com/ASPEC-RACING-PTY-LTD/vad-server => ../../../vad-server
)

require (
	github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop v0.0.0-00010101000000-000000000000
	github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk v0.0.0-00010101000000-000000000000
	github.com/ASPEC-RACING-PTY-LTD/vad-server v0.0.0-00010101000000-000000000000
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-chi/chi/v5 v5.5.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/oschwald/maxminddb-golang/v2 v2.8.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rjeczalik/notify v0.9.3 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.60.1 // indirect
)
