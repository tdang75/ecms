#!/bin/bash
# Generates a self-signed CA and TLS certificates for ECMS backend and frontend.
# Run from the project root: bash generate-certs.sh

set -e

CERT_DIR="./certs"
DAYS=3650
mkdir -p "$CERT_DIR"

cat > "$CERT_DIR/ca.cnf" << 'CNFEOF'
[req]
prompt             = no
distinguished_name = dn
x509_extensions    = v3_ca
[dn]
C  = US
ST = Local
L  = Local
O  = ECMS-CA
CN = ECMS Root CA
[v3_ca]
subjectKeyIdentifier   = hash
authorityKeyIdentifier = keyid:always,issuer
basicConstraints       = critical,CA:true
keyUsage               = critical,keyCertSign,cRLSign
CNFEOF

cat > "$CERT_DIR/frontend.cnf" << 'CNFEOF'
[req]
prompt             = no
distinguished_name = dn
req_extensions     = v3_req
[dn]
C  = US
ST = Local
L  = Local
O  = ECMS
CN = ecms-frontend
[v3_req]
subjectAltName = @alt_names
[alt_names]
DNS.1 = ecms-frontend
DNS.2 = frontend
DNS.3 = localhost
IP.1  = 127.0.0.1
CNFEOF

echo "── Generating CA ────────────────────────────────────────────────────────"
openssl genrsa -out "$CERT_DIR/ca.key" 4096 2>/dev/null
openssl req -new -x509 -days $DAYS \
  -key    "$CERT_DIR/ca.key" \
  -out    "$CERT_DIR/ca.crt" \
  -config "$CERT_DIR/ca.cnf"
echo "✅  $CERT_DIR/ca.crt"

echo "── Generating frontend cert ──────────────────────────────────────────────"
openssl genrsa -out "$CERT_DIR/frontend.key" 2048 2>/dev/null
openssl req -new \
  -key    "$CERT_DIR/frontend.key" \
  -out    "$CERT_DIR/frontend.csr" \
  -config "$CERT_DIR/frontend.cnf"
openssl x509 -req -days $DAYS \
  -in      "$CERT_DIR/frontend.csr" \
  -CA      "$CERT_DIR/ca.crt" \
  -CAkey   "$CERT_DIR/ca.key" \
  -CAcreateserial \
  -out     "$CERT_DIR/frontend.crt" \
  -extensions v3_req \
  -extfile "$CERT_DIR/frontend.cnf" 2>/dev/null
rm -f "$CERT_DIR/frontend.csr"
echo "✅  $CERT_DIR/frontend.crt"

rm -f "$CERT_DIR/ca.key" "$CERT_DIR/ca.srl" "$CERT_DIR/ca.cnf" "$CERT_DIR/frontend.cnf"

echo ""
echo "✅  Certificates written to $CERT_DIR/"
echo ""
echo "Trust the CA so your browser accepts the certs without warnings:"
echo "  macOS:   sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain $CERT_DIR/ca.crt"
echo "  Linux:   sudo cp $CERT_DIR/ca.crt /usr/local/share/ca-certificates/ecms-ca.crt && sudo update-ca-certificates"
echo "  Windows: double-click $CERT_DIR/ca.crt → Install → Local Machine → Trusted Root Certification Authorities"
echo ""
echo "Then rebuild: docker compose up -d --build"
