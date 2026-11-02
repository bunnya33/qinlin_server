# Third-party protocol references

The protocol adapter in `internal/qinlin/client.go` is adapted from and cross-checked against these pinned MIT-licensed sources:

- QinlinOpen, commit `969d2dd6e90085cd83c2aa74944e723e686dee5a`: https://github.com/rponeawa/QinlinOpen/blob/969d2dd6e90085cd83c2aa74944e723e686dee5a/app/src/main/java/top/rpone/qinlinopen/data/QinlinApi.kt
- qinlin-remote-open, commit `5bb01454d041e51106392e53ae33de0f6d2afcad`: https://github.com/ship-it-th1/qinlin-remote-open/blob/5bb01454d041e51106392e53ae33de0f6d2afcad/app/open_door_api.py

Upstream MD5 signing and AES-ECB phone encoding are wire compatibility requirements. Local credentials use random secrets, SHA-256 token hashes and AES-GCM authenticated encryption. These references are unofficial clients and do not imply additional access rights or an official API guarantee.

## QinlinOpen

MIT License

Copyright (c) 2026 Qinlin Open contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## qinlin-remote-open

MIT License

Copyright (c) 2026 qinlin-remote-open contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Go modules

Direct dependencies: `modernc.org/sqlite` (BSD-3-Clause), `github.com/gofrs/flock` (BSD-3-Clause), `golang.org/x/crypto` and `golang.org/x/term` (BSD-3-Clause). SQLite itself is public domain. Their own license notices remain in the Go module distribution; see the exact versions and all indirect modules in `go.mod` and `go.sum`.
