#!/bin/bash
set -e

echo "🚀 Starting E2E Test..."

# Constants
CLUSTER_NAME="kind"
NAMESPACE="default"
RELEASE_NAME="k8s-eni-tagger"
IMAGE_TAG="e2e"
AWS_ENDPOINT="http://localhost:4566"

# 0. Install LocalStack in Kind
echo "📥 Installing LocalStack..."
helm repo add localstack https://localstack.github.io/helm-charts
helm repo update
helm install localstack localstack/localstack --wait

# Port forward LocalStack for the script to use
echo "🔌 Port-forwarding LocalStack..."
kubectl port-forward svc/localstack 4566:4566 > /dev/null 2>&1 &
PF_PID=$!
trap "kill $PF_PID" EXIT
sleep 5 # Wait for port-forward

# 1. Deploy Controller
echo "📦 Deploying Controller..."
helm install $RELEASE_NAME ./charts/k8s-eni-tagger \
  --set image.repository=ghcr.io/prabhu/k8s-eni-tagger \
  --set image.tag=$IMAGE_TAG \
  --set image.pullPolicy=Never \
  --set env.AWS_ACCESS_KEY_ID=test \
  --set env.AWS_SECRET_ACCESS_KEY=test \
  --set env.AWS_REGION=us-east-1 \
  --set env.AWS_ENDPOINT_URL=http://localstack.default.svc.cluster.local:4566 \
  --wait

# 2. Create a Test Pod
echo "🧪 Creating Test Pod..."
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: e2e-test-pod
  annotations:
    eni-tagger.io/tags: "Test=E2E,Environment=CI"
spec:
  containers:
  - name: nginx
    image: nginx:latest
    ports:
    - containerPort: 80
EOF

# 3. Wait for Pod IP
echo "⏳ Waiting for Pod IP..."
kubectl wait --for=condition=Ready pod/e2e-test-pod --timeout=60s
POD_IP=$(kubectl get pod e2e-test-pod -o jsonpath='{.status.podIP}')
echo "✅ Pod IP: $POD_IP"

# 4. Create Mock ENI in LocalStack matching Pod IP
echo "🛠️ Creating Mock ENI in LocalStack..."
# Create VPC
VPC_ID=$(aws --endpoint-url=$AWS_ENDPOINT ec2 create-vpc --cidr-block 10.0.0.0/16 --query 'Vpc.VpcId' --output text)
# Create Subnet
SUBNET_ID=$(aws --endpoint-url=$AWS_ENDPOINT ec2 create-subnet --vpc-id $VPC_ID --cidr-block 10.0.1.0/24 --query 'Subnet.SubnetId' --output text)
# Create ENI with specific IP
ENI_ID=$(aws --endpoint-url=$AWS_ENDPOINT ec2 create-network-interface \
  --subnet-id $SUBNET_ID \
  --private-ip-address $POD_IP \
  --query 'NetworkInterface.NetworkInterfaceId' \
  --output text)
echo "✅ Created ENI: $ENI_ID for IP: $POD_IP"

# 5. Wait for Reconciliation
echo "⏳ Waiting for Controller to tag ENI..."
# We can check the Pod events or query AWS
sleep 10 # Give controller time to reconcile

# 6. Verify Tags
echo "🔍 Verifying Tags..."
TAGS=$(aws --endpoint-url=$AWS_ENDPOINT ec2 describe-tags \
  --filters "Name=resource-id,Values=$ENI_ID" \
  --query 'Tags')

echo "Current Tags: $TAGS"

if echo "$TAGS" | grep -q "Test" && echo "$TAGS" | grep -q "E2E"; then
  echo "✅ Success! Tags found on ENI."
else
  echo "❌ Failure! Tags not found."
  exit 1
fi

echo "🎉 E2E Test Passed!"
