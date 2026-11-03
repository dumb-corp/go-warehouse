package core_http_middleware

import "net/http"

type Middleware (http.Handler) http.Handler
