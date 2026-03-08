package audit

import "github.com/rs/zerolog"

func testLogger() zerolog.Logger {
	return zerolog.Nop()
}
