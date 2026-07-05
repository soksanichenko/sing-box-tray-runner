package assets

import "embed"

//go:embed locales/*.json
var LocaleFS embed.FS
