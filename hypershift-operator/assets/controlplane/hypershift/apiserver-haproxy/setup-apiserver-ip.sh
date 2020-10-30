#!/usr/bin/env bash
set -x
ip addr add {{ .ExternalAPIAddress }}/32 brd {{ .ExternalAPIAddress }} scope host dev lo
ip route add {{ .ExternalAPIAddress }}/32 dev lo scope link src {{ .ExternalAPIAddress }}
