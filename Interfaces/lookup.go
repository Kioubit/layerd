package Interfaces

import (
	"net/netip"
	"sync/atomic"
)

type RouteDetails struct {
	Prefix  netip.Prefix
	NextHop netip.Addr
}

var LookupArray [255]atomic.Pointer[[]RouteDetails]
