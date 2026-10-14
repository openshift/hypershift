# kubernetes-default-proxy addresses

`--listen-addr`, `--proxy-addr`, and `--apiserver-addr` require TCP
`host:port` inputs, not URLs. Prefer `[IPv6]:port` for IPv6, including
zone identifiers such as `[fe80::1%eth0]:6443`. Existing valid addresses
and TCP service names are preserved; zones are not URL-decoded.

For compatibility with existing producers, unbracketed IPv6 is repaired
only when the part before the last colon is IPv6 and the final part is
an explicit decimal port in 0..65535. For example, `fd00::1:6443` becomes
`[fd00::1]:6443`. Raw IPv6 notation is ambiguous in isolation: this repair
relies on these flags' existing host:port contract, not a general-purpose
URI heuristic. No port is guessed, and unbracketed service names are not
repaired. Schemes, userinfo, paths, queries, and fragments are rejected.

All three addresses are checked before network I/O. Errors name the
affected flags without echoing rejected input, and failed validation
does not partially update the configuration.
