#!/usr/bin/env bash
set -x
ip addr delete {{ .InternalAPIAddress }}/32 dev lo
ip route del {{ .InternalAPIAddress }}/32 dev lo scope link src {{ .InternalAPIAddress }}
