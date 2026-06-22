#!/usr/bin/env bash
set -x
ip addr add {{ .InternalAPIAddress }}/32 brd {{ .InternalAPIAddress }} scope host dev lo
ip route add {{ .InternalAPIAddress }}/32 dev lo scope link src {{ .InternalAPIAddress }}
