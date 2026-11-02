package web

import "embed"

//go:embed index.html app.css app.js request-id.js
var Files embed.FS
