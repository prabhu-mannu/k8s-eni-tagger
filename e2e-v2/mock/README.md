# AWS EC2 Mock (e2e-v2)

A minimal HTTP mock for the EC2 API used by k8s-eni-tagger E2E-v2 scenarios. It implements just enough of the EC2 Query API plus a few admin endpoints to seed and inspect ENIs and tags.

## Supported EC2 actions

- `DescribeAccountAttributes` – returns a single `supported-platforms` attribute with value `VPC`.
- `DescribeNetworkInterfaces` – supports a `Filter.1.Name=private-ip-address` filter and returns the seeded ENI that matches the requested IP (including `networkInterfaceId`, `subnetId`, `interfaceType`, `description`, `privateIpAddressesSet`, and `tagSet`).
- `CreateTags` – applies tags to a seeded ENI using `ResourceId.n` and `Tag.n.Key/Tag.n.Value` parameters.
- `DeleteTags` – removes tags from a seeded ENI using `ResourceId.n` and `Tag.n.Key` parameters.

Responses are formatted as AWS Query XML (`http://ec2.amazonaws.com/doc/2016-11-15/`). Errors use simple XML with AWS-style codes (e.g., `InvalidNetworkInterfaceID.NotFound`).

## Admin endpoints

- `POST /admin/enis` – seed an ENI. Body example:
  ```json
  {"eniId":"eni-1234","privateIp":"10.0.1.42","interfaceType":"interface","subnetId":"subnet-1234"}
  ```
- `GET /admin/tags/{eniId}` – return the current tags for an ENI as JSON.
- `GET /healthz` – liveness probe.

## Run locally

```bash
# Build and run
cd e2e-v2/mock
docker build -t aws-mock:dev .
docker run -p 4566:4566 aws-mock:dev

# Seed and query
curl -X POST http://localhost:4566/admin/enis \
  -H 'Content-Type: application/json' \
  -d '{"eniId":"eni-1234","privateIp":"10.0.1.42","interfaceType":"interface","subnetId":"subnet-1234"}'

curl "http://localhost:4566?Action=DescribeNetworkInterfaces&Version=2016-11-15&Filter.1.Name=private-ip-address&Filter.1.Value.1=10.0.1.42"
```

## Notes

- The mock keeps all state in memory; restarting the container clears ENIs and tags.
- Only the actions above are implemented; other EC2 calls will return `InvalidAction`.
- XML responses are intentionally minimal but compatible with the AWS SDK for Go v2 calls the controller uses.
