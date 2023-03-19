package Interfaces

import (
	"log"
	"net"
	"net/netip"
	"strconv"
)
import "github.com/songgao/water"

var interfaceTable [255]*water.Interface

func Startup(l3idList []uint8) {
	outPacketChan := make(chan *txInfo, 10)
	inPacketChan := make(chan *txInfo, 10)
	for i := 0; i < len(l3idList)*4; i++ {
		go txProcessor(outPacketChan)
	}
	for i := 0; i < len(l3idList)*4; i++ {
		go rxProcessor(inPacketChan)
	}
	go rxListener(inPacketChan)
	for _, u := range l3idList {
		go createInterface("layerd"+strconv.Itoa(int(u)), u, outPacketChan)
	}
}

type txInfo struct {
	packet *[]byte
	l3id   uint8
}

func createInterface(interfaceName string, l3id uint8, packetChan chan *txInfo) {
	config := water.Config{
		DeviceType: water.TUN,
	}
	config.Name = interfaceName
	iFace, err := water.New(config)
	if err != nil {
		log.Fatal("Is the 'tun' device available? Failed creating TUN interface ", interfaceName, " - ", err)
	}
	interfaceTable[l3id] = iFace
	for {
		packet := make([]byte, 2000)
		packet[0] = l3id
		n, err := iFace.Read(packet[1:])
		if err != nil {
			log.Fatal("Failed reading from tun interface ", err)
		}
		packet = packet[:n+1]
		packetChan <- &txInfo{
			packet: &packet,
			l3id:   l3id,
		}
	}
}

func txProcessor(packetChan chan *txInfo) {
	for {
		pkt := <-packetChan
		if len(*pkt.packet) < 41 {
			continue
		}
		var dst netip.Addr
		switch parseIPVersion((*pkt.packet)[1]) {
		case 4:
			dst = netip.AddrFrom4([4]byte((*pkt.packet)[17:21]))
		case 6:
			dst = netip.AddrFrom16([16]byte((*pkt.packet)[25:41]))
		}
		tDst := LookupArray[pkt.l3id].Load()
		if tDst == nil {
			continue
		}
		destinations := *tDst
		var target *netip.Addr
		for i := 0; i < len(destinations); i++ {
			if destinations[i].Prefix.Contains(dst) {
				target = &destinations[i].NextHop
				break
			}
		}
		if target == nil {
			continue
		}
		tb := target.As16()
		destinationIP := &net.UDPAddr{
			IP:   net.IP(tb[:]),
			Port: 3643,
		}
		conn, err := net.DialUDP("udp6", nil, destinationIP)
		if err != nil {
			continue
		}
		_, _ = conn.Write(*pkt.packet)
	}
}

func rxListener(packetChan chan *txInfo) {
	addr := net.UDPAddr{
		IP:   nil,
		Port: 3643,
	}
	ser, err := net.ListenUDP("udp6", &addr)
	if err != nil {
		panic(err)
	}
	for {
		packetBuf := make([]byte, 2000)
		readLen, _, err := ser.ReadFromUDP(packetBuf)
		if err != nil {
			continue
		}
		packet := packetBuf[1:readLen]
		l3id := packetBuf[0]
		packetChan <- &txInfo{
			packet: &packet,
			l3id:   l3id,
		}
	}
}

func rxProcessor(packetChan chan *txInfo) {
	for {
		pkt := <-packetChan
		dest := interfaceTable[pkt.l3id]
		if dest == nil {
			continue
		}
		_, _ = dest.Write(*pkt.packet)
	}
}

func parseIPVersion(v byte) int {
	if uint8(v>>4^byte(6)) == 0 {
		return 6
	} else if uint8(v>>4^byte(4)) == 0 {
		return 4
	}
	return 0
}
