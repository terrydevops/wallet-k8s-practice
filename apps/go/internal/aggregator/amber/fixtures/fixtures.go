// Package fixtures holds response bodies shaped like the Amber API documentation. They are used by
// unit tests and served by mock-quote-api. Values are illustrative, not market data.
package fixtures

import "embed"

//go:embed *.json
var FS embed.FS
