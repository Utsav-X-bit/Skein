# Network namespace integration test

Three namespaces prove the core claim: an IPv4-only node and an IPv6-only node
can talk through a dual-stack member.

| Namespace | Addresses | Meaning |
|---|---|---|
| `n4` | `192.168.10.1/24`, IPv6 disabled | IPv4-only underlay |
| `n6` | `fd00:10::1/64`, no IPv4 address | IPv6-only underlay |
| `ndual` | `192.168.10.2/24`, `fd00:10::2/64` | dual-stack bridge |

Overlay addresses are `10.144.144.1` (n4), `10.144.144.3` (n6), and
`10.144.144.2` (ndual). `ndual` forwards between the two.

## Run

```bash
sudo ./test/netns/run.sh
```

The script:

1. generates a per-run prefix `skein-<uid>-<8 hex>`;
2. creates the topology with `setup.sh`;
3. runs `go test ./internal/integration -run TestNetns -v` as root with
   `SKEIN_NETNS_PREFIX` set;
4. always runs `cleanup.sh`, which deletes only the three namespaces carrying
   that exact prefix.

## What the test asserts

- `n4` and `ndual` form a direct IPv4 path.
- `n6` and `ndual` form a direct IPv6 path.
- `n4` reaches `10.144.144.3` through `ndual`, with `to_next != to_final`.
- `ping` from `n4` to the `n6` overlay address succeeds.
- Cleanup leaves no namespace with the run prefix.

The test skips (rather than fails) when it is not run as root or when
`SKEIN_NETNS_PREFIX` is unset, so `go test ./...` stays runnable everywhere.
