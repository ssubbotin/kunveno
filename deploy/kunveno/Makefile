# The kunveno stack. Run in this directory.
#   make up            start the stack, or update a running one to the images of KUNVENO_VERSION
#   make down          stop and remove the containers; the volumes (rooms, notes, recordings) stay
#   make pull          fetch the images without restarting anything
#   make import-data   copy ./data of a release before 0.4.5 into the data volume, once, then make up
# SERVICES limits pull and up to some services, e.g. make up SERVICES="server web" (an add-on whose
# private image needs a registry login can be left out this way).

SERVICES ?=

.PHONY: up down pull import-data

up: pull
	docker compose up -d --remove-orphans $(SERVICES)

down:
	docker compose down

pull:
	docker compose pull $(SERVICES)

# Releases before 0.4.5 kept rooms created in the lobby, meeting summaries, notes and recordings in
# ./data; they now live in the data volume. Stops the server and the player, copies ./data over
# what the volume holds and gives it to the services' user, 10001; then run make up.
import-data:
	@test -d data || { echo "no ./data here: nothing to import"; exit 1; }
	docker compose stop server player
	docker compose run --rm --no-deps --user 0 --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER \
		--entrypoint sh -v "$(CURDIR)/data:/old:ro" server \
		-c 'cp -r /old/. /var/lib/kunveno/ && chown -R 10001:10001 /var/lib/kunveno'
	@echo "imported: run make up; remove ./data once the rooms, notes and recordings show up"
