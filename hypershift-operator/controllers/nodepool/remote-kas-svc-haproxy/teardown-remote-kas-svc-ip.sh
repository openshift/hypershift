#!/usr/bin/env bash
set -x
ip addr delete {{ .RemoteKASSVCAdress }}/32 dev lo
ip route del {{ .RemoteKASSVCAdress }}/32 dev lo scope link src {{ .RemoteKASSVCAdress }}
