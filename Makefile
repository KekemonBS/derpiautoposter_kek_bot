all: build save
build:
	docker buildx build -t derpiautoposter_kek_bot -f RPIDockerfile .
save:
	docker save -o ./derpiautoposter_kek_bot.tar derpiautoposter_kek_bot
