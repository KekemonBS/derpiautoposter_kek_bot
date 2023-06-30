all: build save
build:
	docker buildx build --platform linux/arm/v7 -t derpiautoposter_kek_bot -f RPIDockerfile .
save:
	docker save -o ./derpiautoposter_kek_bot.tar derpiautoposter_kek_bot
