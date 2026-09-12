#!/bin/bash
set -e
iptables -N NOEBS_MOJALOOP 2>/dev/null || true
iptables -F NOEBS_MOJALOOP
iptables -A NOEBS_MOJALOOP -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -A NOEBS_MOJALOOP -p tcp --dport 4040 -j ACCEPT
iptables -A NOEBS_MOJALOOP -p icmp -j ACCEPT
iptables -A NOEBS_MOJALOOP -j DROP
iptables -C INPUT -i noebsml -s 172.30.250.1 -j NOEBS_MOJALOOP 2>/dev/null || iptables -I INPUT -i noebsml -s 172.30.250.1 -j NOEBS_MOJALOOP
iptables -C FORWARD -i noebsml -s 172.30.250.1 -j DROP 2>/dev/null || iptables -I FORWARD -i noebsml -s 172.30.250.1 -j DROP
iptables -C FORWARD -i noebsml -s 172.30.250.1 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT 2>/dev/null || iptables -I FORWARD -i noebsml -s 172.30.250.1 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
