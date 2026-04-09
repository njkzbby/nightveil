// Package xhttp re-exports XHTTP transport for external consumers.
package xhttp

import "github.com/njkzbby/nightveil/internal/transport/xhttp"

type Config = xhttp.Config
type Client = xhttp.Client

var NewClient = xhttp.NewClient
