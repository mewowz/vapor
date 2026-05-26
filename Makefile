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
		internal/proto/steam/steammessages_clientserver.proto \
		internal/proto/steam/encrypted_app_ticket.proto

.PHONY: emsg
emsg: ENUM_FILE = emsg_enums.go
emsg:
	protoc \
		-I internal/proto/steam \
		--go_out=. \
		--go_opt=paths=source_relative \
		--go_opt=Menums_clientserver.proto=$(REPO) \
		internal/proto/steam/enums_clientserver.proto
	@echo "package vapor" > $(ENUM_FILE)
	@echo -e "\ntype EMsg uint32\n" >> $(ENUM_FILE)
	@echo "const (" >> $(ENUM_FILE)
	@grep -E "^\s+k_EMsg[A-Za-z0-9_]+ = [0-9]+" internal/proto/steam/enums_clientserver.proto | \
		sed -E 's/[[:space:]]*k_EMsg([A-Za-z0-9_]+)[[:space:]]*=[[:space:]]*([0-9]+).*/\tEMsg\1 EMsg = \2/' >> $(ENUM_FILE)
	@echo ")" >> $(ENUM_FILE)
	@gofmt -w $(ENUM_FILE)
	rm enums_clientserver.pb.go
