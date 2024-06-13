#!/usr/bin/env bash
set -x
ip addr add {{ .RemoteKASSVCAdress }}/32 brd {{ .RemoteKASSVCAdress }} scope host dev lo
ip route add {{ .RemoteKASSVCAdress }}/32 dev lo scope link src {{ .RemoteKASSVCAdress }}
