//go:build !linux

package tun

// transportOffset is zero on platforms whose TUN API does not use virtio-net
// headers.
const transportOffset = 0
