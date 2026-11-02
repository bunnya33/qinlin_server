package web

import "embed"

//go:embed index.html app.css app.js request-id.js maintenance.js
var Files embed.FS
