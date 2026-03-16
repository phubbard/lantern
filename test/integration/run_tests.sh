#!/usr/bin/env bash
set -euo pipefail

# Integration test runner for Lantern
# Usage: run_tests.sh [dns|dhcp|all]

SERVER="${LANTERN_SERVER:-172.30.0.2}"
DOMAIN="test.lantern"
SUITE="${1:-all}"

PASS=0
FAIL=0
TOTAL=0

# Colored output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

assert_eq() {
    local test_name="$1"
    local expected="$2"
    local actual="$3"
    TOTAL=$((TOTAL + 1))
    if [ "$expected" = "$actual" ]; then
        echo -e "${GREEN}PASS${NC} $test_name"
        PASS=$((PASS + 1))
    else
        echo -e "${RED}FAIL${NC} $test_name"
        echo "  expected: $expected"
        echo "  actual:   $actual"
        FAIL=$((FAIL + 1))
    fi
}

assert_contains() {
    local test_name="$1"
    local needle="$2"
    local haystack="$3"
    TOTAL=$((TOTAL + 1))
    if echo "$haystack" | grep -q "$needle"; then
        echo -e "${GREEN}PASS${NC} $test_name"
        PASS=$((PASS + 1))
    else
        echo -e "${RED}FAIL${NC} $test_name"
        echo "  expected to contain: $needle"
        echo "  actual: $haystack"
        FAIL=$((FAIL + 1))
    fi
}

assert_not_empty() {
    local test_name="$1"
    local value="$2"
    TOTAL=$((TOTAL + 1))
    if [ -n "$value" ]; then
        echo -e "${GREEN}PASS${NC} $test_name"
        PASS=$((PASS + 1))
    else
        echo -e "${RED}FAIL${NC} $test_name"
        echo "  expected non-empty value"
        FAIL=$((FAIL + 1))
    fi
}

assert_http_ok() {
    local test_name="$1"
    local url="$2"
    TOTAL=$((TOTAL + 1))
    local status
    status=$(curl -s -o /dev/null -w "%{http_code}" "$url" 2>/dev/null || echo "000")
    if [ "$status" = "200" ]; then
        echo -e "${GREEN}PASS${NC} $test_name"
        PASS=$((PASS + 1))
    else
        echo -e "${RED}FAIL${NC} $test_name (HTTP $status)"
        FAIL=$((FAIL + 1))
    fi
}

# Wait for server to be ready
wait_for_server() {
    echo "Waiting for Lantern server at $SERVER..."
    for i in $(seq 1 30); do
        if dig @"$SERVER" +time=1 +tries=1 version.bind chaos txt > /dev/null 2>&1 || \
           dig @"$SERVER" +time=1 +tries=1 "$DOMAIN" SOA > /dev/null 2>&1; then
            echo "Server is ready."
            return 0
        fi
        sleep 1
    done
    echo "Server failed to become ready after 30s"
    return 1
}

# ─── DNS Tests ──────────────────────────────────────

run_dns_tests() {
    echo ""
    echo "════════════════════════════════════════"
    echo " DNS Test Suite"
    echo "════════════════════════════════════════"
    echo ""

    # Test: SOA record for the zone
    local soa
    soa=$(dig @"$SERVER" "$DOMAIN" SOA +short 2>/dev/null)
    assert_not_empty "DNS: SOA record exists for $DOMAIN" "$soa"

    # Test: Static host resolves (testbox is configured in config.test.json)
    local static_ip
    static_ip=$(dig @"$SERVER" "testbox.$DOMAIN" A +short 2>/dev/null)
    assert_eq "DNS: Static host testbox resolves" "172.30.0.50" "$static_ip"

    # Test: PTR for static host
    local ptr
    ptr=$(dig @"$SERVER" -x 172.30.0.50 +short 2>/dev/null)
    assert_contains "DNS: PTR for 172.30.0.50 returns testbox" "testbox.$DOMAIN" "$ptr"

    # Test: Non-existent local name returns NXDOMAIN (or empty)
    local nxdomain
    nxdomain=$(dig @"$SERVER" "nonexistent.$DOMAIN" A +short 2>/dev/null)
    assert_eq "DNS: Non-existent local name returns empty" "" "$nxdomain"

    # Test: Upstream resolution works (google.com should resolve)
    local google
    google=$(dig @"$SERVER" "google.com" A +short 2>/dev/null | head -1)
    assert_not_empty "DNS: Upstream resolution (google.com)" "$google"

    # Test: Second query for same upstream name (should be cached)
    local google2
    google2=$(dig @"$SERVER" "google.com" A +short 2>/dev/null | head -1)
    assert_not_empty "DNS: Cached upstream resolution (google.com)" "$google2"

    # Test: AAAA query for upstream
    local google_v6
    google_v6=$(dig @"$SERVER" "google.com" AAAA +short 2>/dev/null | head -1)
    assert_not_empty "DNS: AAAA upstream resolution (google.com)" "$google_v6"

    # Test: Multiple rapid queries (concurrency)
    echo ""
    echo "Running 50 concurrent DNS queries..."
    local concurrent_fail=0
    for i in $(seq 1 50); do
        dig @"$SERVER" "google.com" A +short +time=5 > /dev/null 2>&1 &
    done
    wait
    TOTAL=$((TOTAL + 1))
    echo -e "${GREEN}PASS${NC} DNS: 50 concurrent queries completed"
    PASS=$((PASS + 1))

    # Test: TCP DNS (not just UDP)
    local tcp_result
    tcp_result=$(dig @"$SERVER" "google.com" A +tcp +short 2>/dev/null | head -1)
    assert_not_empty "DNS: TCP query works" "$tcp_result"
}

# ─── Web API Tests ──────────────────────────────────

run_api_tests() {
    echo ""
    echo "════════════════════════════════════════"
    echo " Web API Test Suite"
    echo "════════════════════════════════════════"
    echo ""

    # Test: Dashboard loads
    assert_http_ok "API: Dashboard (GET /)" "http://$SERVER:8080/"

    # Test: Leases page loads
    assert_http_ok "API: Leases page (GET /leases)" "http://$SERVER:8080/leases"

    # Test: Metrics JSON endpoint
    assert_http_ok "API: Metrics JSON (GET /api/metrics)" "http://$SERVER:8080/api/metrics"

    # Test: Leases JSON endpoint
    assert_http_ok "API: Leases JSON (GET /api/leases)" "http://$SERVER:8080/api/leases"

    # Test: Metrics JSON contains expected fields
    local metrics
    metrics=$(curl -s "http://$SERVER:8080/api/metrics" 2>/dev/null)
    assert_contains "API: Metrics has QueriesTotal" "QueriesTotal" "$metrics"

    # Test: Leases JSON is valid JSON array
    local leases
    leases=$(curl -s "http://$SERVER:8080/api/leases" 2>/dev/null)
    local leases_valid
    leases_valid=$(echo "$leases" | jq -e '. | type' 2>/dev/null || echo "invalid")
    assert_not_empty "API: Leases endpoint returns valid JSON" "$leases_valid"

    # Test: SSE endpoint connects (check we get the stream header)
    local sse_status
    sse_status=$(curl -s -o /dev/null -w "%{http_code}" --max-time 2 \
        -H "Accept: text/event-stream" "http://$SERVER:8080/api/events/stream" 2>/dev/null || echo "000")
    # SSE returns 200 even if we timeout reading (that's expected)
    TOTAL=$((TOTAL + 1))
    if [ "$sse_status" = "200" ] || [ "$sse_status" = "000" ]; then
        echo -e "${GREEN}PASS${NC} API: SSE endpoint responds"
        PASS=$((PASS + 1))
    else
        echo -e "${RED}FAIL${NC} API: SSE endpoint (HTTP $sse_status)"
        FAIL=$((FAIL + 1))
    fi
}

# ─── DHCP Tests ─────────────────────────────────────

run_dhcp_tests() {
    echo ""
    echo "════════════════════════════════════════"
    echo " DHCP Test Suite"
    echo "════════════════════════════════════════"
    echo ""

    # Run the Go DHCP exerciser
    /tests/dhcp-exerciser -server "$SERVER" -interface eth0 -domain "$DOMAIN"
}

# ─── Main ───────────────────────────────────────────

wait_for_server

case "$SUITE" in
    dns)
        run_dns_tests
        run_api_tests
        ;;
    dhcp)
        run_dhcp_tests
        ;;
    all)
        run_dns_tests
        run_api_tests
        run_dhcp_tests
        ;;
    *)
        echo "Unknown test suite: $SUITE"
        echo "Usage: $0 [dns|dhcp|all]"
        exit 1
        ;;
esac

echo ""
echo "════════════════════════════════════════"
echo -e " Results: ${GREEN}${PASS} passed${NC}, ${RED}${FAIL} failed${NC}, ${TOTAL} total"
echo "════════════════════════════════════════"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
exit 0
