REPO := github.com/mewowz/vapor
.PHONY: proto
proto:
	mkdir -p internal/steamproto
	protoc \
		-I internal/proto/steam \
		--go_out=internal/steamproto \
		--go_opt=paths=source_relative \
		--go_opt=Msteammessages_base.proto=$(REPO)/internal/steamproto \
		--go_opt=Msteammessages_clientserver_login.proto=$(REPO)/internal/steamproto \
		--go_opt=Msteammessages_clientserver.proto=$(REPO)/internal/steamproto \
		--go_opt=Mencrypted_app_ticket.proto=$(REPO)/internal/steamproto \
		internal/proto/steam/steammessages_base.proto \
		internal/proto/steam/steammessages_clientserver_login.proto \
		internal/proto/steam/steammessages_clientserver.proto
