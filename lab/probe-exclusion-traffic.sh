#!/bin/sh
set -eu
cd /tmp

for name in direct intercept; do
    mkdir "$name"
    echo "$name" > "$name/probe.txt"
done
openssl req -x509 -newkey rsa:2048 -nodes -subj /CN=traffic.example \
    -keyout key -out cert -days 1 >/dev/null 2>&1
(cd direct && exec openssl s_server -quiet -WWW -accept 18443 -key ../key -cert ../cert) >direct.log 2>&1 &
(cd intercept && exec openssl s_server -quiet -WWW -accept 18444 -key ../key -cert ../cert) >intercept.log 2>&1 &

script=/opt/etc/init.d/S05xkeen
sed -n '/^load_user_ipset_family() {/,/^}/p; /^    add_ipset_exclude() {/,/^    }/p' "$script" > functions
. ./functions
chain=lab_exclusion
comment=
ipt() { "$driver" -t nat "$@"; }

fetch() {
    curl --noproxy '*' --silent --fail --insecure --connect-timeout 2 --max-time 4 "https://$1:18443/probe.txt"
}

check_route() {
    address=$1
    expected=$2
    endpoint=$address
    if [ "$driver" = ip6tables ]; then endpoint="[$address]"; fi
    actual=$(fetch "$endpoint")
    if [ "$actual" != "$expected" ]; then
        echo "unexpected route for $address: expected $expected, observed $actual" >&2
        exit 1
    fi
    echo "$driver $address: $actual"
}

check_family() {
    driver=$1
    family=$driver
    first=$2
    second=$3
    set_family=$4
    name=$5
    pattern=$6
    ipt -N "$chain"
    add_ipset_exclude user_exclude hash:net
    ipt -A "$chain" -p tcp -j REDIRECT --to-ports 18444
    ipt -A OUTPUT -p tcp --dport 18443 -j "$chain"
    for selected in "$first" "$second"; do
        echo "$selected" > list
        load_user_ipset_family "$name" "$set_family" "$pattern" /tmp/list
        for target in "$first" "$second"; do
            expected=intercept
            if [ "$selected" = "$target" ]; then expected=direct; fi
            check_route "$target" "$expected"
        done
    done
    : > list
    load_user_ipset_family "$name" "$set_family" "$pattern" /tmp/list
    check_route "$first" intercept
    check_route "$second" intercept
}

ip addr add 192.0.2.10/32 dev lo
ip addr add 192.0.2.11/32 dev lo
ip -6 addr add 2001:db8::10/128 dev lo nodad
ip -6 addr add 2001:db8::11/128 dev lo nodad
# Wait for both listeners before installing any NAT rule.
for port in 18443 18444; do
    curl --noproxy '*' --silent --fail --insecure --retry 5 --retry-connrefused \
        --retry-delay 1 --max-time 4 "https://127.0.0.1:$port/probe.txt" >/dev/null
done
check_family iptables 192.0.2.10 192.0.2.11 inet user_exclude '([0-9]{1,3}\.){3}[0-9]{1,3}(/[0-9]{1,2})?'
check_family ip6tables 2001:db8::10 2001:db8::11 inet6 user_exclude6 '([0-9a-fA-F]{0,4}:){1,7}[0-9a-fA-F]{0,4}(/[0-9]{1,3})?'
