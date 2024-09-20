```
Install : docker-buildx, docker
docker buildx create --platform linux/amd64,linux/arm64
docker buildx ls
docker buildx use <build_container>
go mod vendor
make
```
