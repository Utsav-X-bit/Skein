//go:build linux

package tun

// transportOffset is the virtio-net header size the Linux TUN device expects
// before every packet (struct virtio_net_hdr).
const transportOffset = 10
