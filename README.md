# oci-photos - GopherCon 2024

GopherCon 2024 Talk 



```sh
$ docker compose up 
```

```
watch --color 'oras manifest fetch localhost:8080/siva/photos/pineapple:latest | jq'
```

```
oras push localhost:8080/siva/photos/pineapple:latest ./pineapple.jpg:image/jpeg ./landscape.jpeg:image/jpeg --artifact-type="application/vnd.acme.photo.v1"
```


```
oras push localhost:8080/siva/photos/pineapple:latest ./pineapple.jpg:image/jpeg ./landscape.jpeg:image/jpeg  --artifact-type="application/vnd.acme.photo.v1"  --annotation "notification.manifest.description=true"
```


```
oras cp localhost:8080/siva/photos/pineapple:latest ttl.sh/siva/photos/pineapple:latest
```
