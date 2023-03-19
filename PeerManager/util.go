package PeerManager

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"net"
	"net/netip"
	"strings"
)

func getAllInterfaces() []string {
	l, err := net.Interfaces()
	if err != nil {
		panic(err)
	}
	result := make([]string, 0, len(l))
	for _, i := range l {
		result = append(result, i.Name)
	}
	return result
}

func generateNodeID() uint64 {
	buf := make([]byte, 8)
	_, err := rand.Read(buf)
	if err != nil {
		panic(err)
	}
	return binary.LittleEndian.Uint64(buf)
}

func IPv6ToByte(ip netip.Addr) []byte {
	result := make([]byte, 16, 16)
	b := ip.As16()
	result = b[:]
	return result
}
func ByteToIPv6(b []byte) netip.Addr {
	return netip.AddrFrom16([16]byte(b))
}

func NodeIDToString(nodeID uint64) string {
	var buf = make([]byte, 8, 8)
	binary.BigEndian.PutUint64(buf, nodeID)
	return strings.ToUpper(hex.EncodeToString(buf))
}
