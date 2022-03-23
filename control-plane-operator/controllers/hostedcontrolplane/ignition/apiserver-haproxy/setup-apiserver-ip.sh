#!/usr/bin/env bash
set -xeuo puipefail



CHAIN_NAME="apiserver-vips"
# Create a chan if it doesn't exist
ensure_chain4() {
    local table="${1}"
    local chain="${2}"
    if ! iptables -w -t "${table}" -S "${chain}" &> /dev/null ; then
        iptables -w -t "${table}" -N "${chain}";
    fi;
}
# Create a chain if it doesn't exist
ensure_chain6() {
    if [ ! -f /proc/net/if_inet6 ]; then
        return
    fi
    local table="${1}"
    local chain="${2}"
    if ! ip6tables -w -t "${table}" -S "${chain}" &> /dev/null ; then
        ip6tables -w -t "${table}" -N "${chain}";
    fi;
}
ensure_rule4() {
    local table="${1}"
    local chain="${2}"
    shift 2
    if ! iptables -w -t "${table}" -C "${chain}" "$@" &> /dev/null; then
        iptables -w -t "${table}" -A "${chain}" "$@"
    fi
}
ensure_rule6() {
    if [ ! -f /proc/net/if_inet6 ]; then
        return
    fi
    local table="${1}"
    local chain="${2}"
    shift 2
    if ! ip6tables -w -t "${table}" -C "${chain}" "$@" &> /dev/null; then
        ip6tables -w -t "${table}" -A "${chain}" "$@"
    fi
}
# set the chain, ensure entry rules, ensure ESTABLISHED rule
initialize() {
    ensure_chain4 nat "${CHAIN_NAME}"
    ensure_chain6 nat "${CHAIN_NAME}"
    ensure_rule4 nat OUTPUT -m comment --comment 'LB vip overriding for local clients' -j ${CHAIN_NAME}
    ensure_rule6 nat OUTPUT -m comment --comment 'LB vip overriding for local clients' -j ${CHAIN_NAME}
    # Need this so that existing flows (with an entry in conntrack) continue,
    ensure_rule4 filter OUTPUT -m comment --comment 'azure LB vip existing' -m addrtype ! --dst-type LOCAL -m state --state ESTABLISHED,RELATED -j ACCEPT
    ensure_rule6 filter OUTPUT -m comment --comment 'azure LB vip existing' -m addrtype ! --dst-type LOCAL -m state --state ESTABLISHED,RELATED -j ACCEPT
}

add_rules() {
    local vip="{{ .ExternalAPIAddress }}"
    local vipport="{{ .ExternalAPIPort }}"
    local bip="{{ .InternalAPIAddress }}"
    local bport="{{ .InternalAPIPort }}"

    # Set up iptables rules
    # add the Internal API address as an additional address on the loopback device
    if [[ "${vip}" =~ : ]]; then
        ensure_rule6 nat "${CHAIN_NAME}" --dst "${vip}" -m tcp -p tcp --dport "${vipport}" -j DNAT --to-destination "[${bip}]:${bport}"
        ip -6 addr add "${bip}"/128 brd "${bip}" scope host dev lo
        ip -6 route add "${bip}"/128 dev lo scope link src "${bip}"
    else
        ensure_rule4 nat "${CHAIN_NAME}" --dst "${vip}" -m tcp -p tcp --dport "${vipport}" -j DNAT --to-destination "${bip}:${bport}"
        ip addr add "${bip}"/32 brd "${bip}" scope host dev lo
        ip route add "${bip}"/32 dev lo scope link src "${bip}"
    fi
}

initialize
add_rules
echo "done applying apiserver vip rules"