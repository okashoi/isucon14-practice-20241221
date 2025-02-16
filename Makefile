.PHONY: *

gogo: stop-services build logs/clear start-services

stop-services:
	sudo systemctl stop nginx
	sudo systemctl stop isuride-go.service
	sudo systemctl stop isuride-matcher.service
	sudo systemctl stop isuride-payment_mock.service
	ssh isucon-s2 sudo systemctl stop mysql

build:
	cd go/ && go build -o isuride

logs: limit=10000
logs:
	journalctl -ex --since "$(shell systemctl status isuride-go.service | grep "Active:" | awk '{print $$6, $$7}')" -n $(limit)

logs/clear:
	sudo journalctl --vacuum-size=1K
	ssh isucon-s2 sudo journalctl --vacuum-size=1K
	sudo truncate --size 0 /var/log/nginx/access.log
	sudo truncate --size 0 /var/log/nginx/error.log
	ssh isucon-s2 sudo truncate --size 0 /var/log/mysql/mysql-slow.log && ssh isucon-s2 sudo chmod 666 /var/log/mysql/mysql-slow.log
	ssh isucon-s2 sudo truncate --size 0 /var/log/mysql/error.log

start-services:
	ssh isucon-s2 sudo systemctl start mysql
	sudo systemctl start isuride-payment_mock.service
	sudo systemctl start isuride-matcher.service
	sudo systemctl start isuride-go.service
	sudo systemctl start nginx

kataribe: timestamp=$(shell TZ=Asia/Tokyo date "+%Y%m%d-%H%M%S")
kataribe:
	mkdir -p ~/kataribe-logs
	sudo cp /var/log/nginx/access.log /tmp/last-access.log && sudo chmod 0666 /tmp/last-access.log
	cat /tmp/last-access.log | kataribe -conf kataribe.toml > ~/kataribe-logs/$$timestamp.log
	cat ~/kataribe-logs/$$timestamp.log | grep --after-context 20 "Top 20 Sort By Total"

pprof: TIME=60
pprof: PROF_FILE=~/pprof.samples.$(shell TZ=Asia/Tokyo date +"%H%M").$(shell git rev-parse HEAD | cut -c 1-8).pb.gz
pprof:
	curl -sSf "http://localhost:6060/debug/fgprof?seconds=$(TIME)" > $(PROF_FILE)
	go tool pprof $(PROF_FILE)

bench:
	ssh isucon-bench "./bench run . run --addr 13.114.103.165:443 --target https://isuride.xiv.isucon.net --payment-url http://172.31.35.89:12346 --payment-bind-port 12346"

