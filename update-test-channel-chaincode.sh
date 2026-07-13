#!/bin/bash
set -e

# Configuration
export CHAINCODE_NAME=h2go-cc
export CHAINCODE_LABEL=h2go-cc
export VERSION="1.19.0"
export SEQUENCE=1
export DOCKER_IMAGE="adriianfdz/h2go-cc:v1.19.0"

echo "🔨 Building chaincode binary..."
cd h2go-chaincodes
go build -o h2go-chaincodes main.go
cd ..

echo "🐋 Building Docker image..."
cd h2go-chaincodes
docker build -t $DOCKER_IMAGE .
echo "📤 Pushing Docker image..."
docker push $DOCKER_IMAGE
cd ..

echo "📦 Creating chaincode package..."
rm -f code.tar.gz chaincode.tgz metadata.json connection.json

cat << METADATA-EOF > "metadata.json"
{
    "type": "ccaas",
    "label": "${CHAINCODE_LABEL}"
}
METADATA-EOF

cat > "connection.json" <<CONN_EOF
{
  "address": "${CHAINCODE_NAME}:7052",
  "dial_timeout": "10s",
  "tls_required": false
}
CONN_EOF

tar cfz code.tar.gz connection.json
tar cfz chaincode.tgz metadata.json code.tar.gz

export PACKAGE_ID=$(kubectl hlf chaincode calculatepackageid --path=chaincode.tgz --language=golang --label=h2go-cc)
echo "📋 Package ID: $PACKAGE_ID"

echo "📥 Installing chaincode on all peers..."
echo "  → dev-peer0"
kubectl hlf chaincode install --path=./chaincode.tgz \
    --config=blockchain/resources/test-network.yaml --language=golang --label=h2go-cc --user=admin --peer=dev-peer0.default

echo "🔄 Syncing external chaincode..."
kubectl hlf externalchaincode sync \
    --image=$DOCKER_IMAGE \
    --image-pull-secret=regcred \
    --name=${CHAINCODE_NAME} \
    --namespace=default \
    --package-id=${PACKAGE_ID} \
    --tls-required=false \
    --replicas=1

echo "✅ Approving chaincode for all organizations..."
echo "  → dev-peer0"
kubectl hlf chaincode approveformyorg --config=blockchain/resources/test-network.yaml --user=admin --peer=dev-peer0.default \
    --package-id=$PACKAGE_ID \
    --version "$VERSION" --sequence "$SEQUENCE" --name=h2go-cc \
    --policy="OR('DevMSP.member', 'OrdererMSP.member')" --channel=test

echo "🎯 Committing chaincode..."
kubectl hlf chaincode commit --config=blockchain/resources/test-network.yaml --user=admin --mspid=DevMSP \
    --version "$VERSION" --sequence "$SEQUENCE" --name=h2go-cc \
    --policy="OR('DevMSP.member', 'OrdererMSP.member')" --channel=test

echo "✨ Chaincode updated successfully!"
echo "📋 Package ID: $PACKAGE_ID"
echo "🏷️  Version: $VERSION"
echo "🔢 Sequence: $SEQUENCE"
