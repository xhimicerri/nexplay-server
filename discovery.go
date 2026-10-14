package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
)

// LAN discovery: the apps broadcast "NEXPLAY_DISCOVER" on UDP 7359 and every server on the network
// answers with its name and address, so users can pick their server without typing an IP.
const discoveryPort = 7359

func RunDiscovery(a *App) {
	pc, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", discoveryPort))
	if err != nil {
		log.Printf("discovery disabled: %v", err)
		return
	}
	defer pc.Close()
	buf := make([]byte, 512)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(string(buf[:n])), "NEXPLAY_DISCOVER") {
			continue
		}
		host := localIPFor(addr)
		reply, _ := json.Marshal(map[string]any{"server": "nexplay", "name": a.serverName(), "address": fmt.Sprintf("http://%s:%d", host, a.Port), "version": Version})
		_, _ = pc.WriteTo(reply, addr)
	}
}

// localIPFor picks this computer's address on the same network as the asking device.
func localIPFor(addr net.Addr) string {
	ua, ok := addr.(*net.UDPAddr)
	if ok {
		if c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: ua.IP, Port: 9}); err == nil {
			defer c.Close()
			return c.LocalAddr().(*net.UDPAddr).IP.String()
		}
	}
	if l := LocalAddresses(); len(l) > 0 {
		return l[0]
	}
	return "127.0.0.1"
}
