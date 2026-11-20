# Dockerfile.axm 의 마지막(런타임) 단계와 같다. 다른 곳은 바이너리를 밖에서 만들어 넣는 한 줄뿐이다(LST-51).
# test_axm.py 가 둘이 같은지 확인한다.
FROM alpine:3.23.4
RUN apk --no-cache add ca-certificates tzdata shadow su-exec
WORKDIR /listmonk
COPY listmonk .
COPY config.toml.sample config.toml
COPY LICENSE README.md FORK.md ./
COPY docker-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/docker-entrypoint.sh
EXPOSE 9000
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["./listmonk"]
