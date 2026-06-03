# OBS Lower Third

En lättviktig Go-server som visar dynamiska lower thirds i OBS via Browser Source.

## Projektstruktur

```text
.
├── go.mod
├── main.go
├── main_test.go
├── templates/
│   └── overlay.html
└── assets/
    └── logos/
        ├── README.md
        ├── s.png
        ├── m.png
        └── ...
```

## Starta servern

```bash
go run .
```

Servern lyssnar som standard på:

```text
http://localhost:8080
```

Du kan välja port från kommandoraden med `-port`:

```bash
go run . -port 9090
```

Då använder du exempelvis följande OBS-URL:

```text
http://localhost:9090/
```

## OBS Browser Source

Lägg till en Browser Source i OBS med URL:

```text
http://localhost:8080/
```

Rekommenderad storlek:

```text
1920x1080
```

Sätt gärna Browser Source-bakgrunden transparent, vilket OBS normalt hanterar automatiskt eftersom sidan själv har transparent body.

## Logotyper

Lägg PNG-logotyper i:

```text
assets/logos/
```

Filnamnet ska matcha `party` i API-anropet. Exempel: `"party":"s"` laddar:

```text
/assets/logos/s.png
```

Om filen saknas döljs logotypytan automatiskt.

## API

### Visa lower third

```bash
curl -X POST http://localhost:8080/api/show \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "Förnamn Efternamn",
    "party": "s",
    "title": "Kommunstyrelsens ordförande",
    "duration_in": 500,
    "duration_out": 500
  }'
```

`title`, `party`, `duration_in` och `duration_out` är valfria. Om `title` saknas eller är tom döljs titelraden helt.

### Dölj lower third

```bash
curl -X POST http://localhost:8080/api/hide \
  -H 'Content-Type: application/json' \
  -d '{"duration_out": 500}'
```

`duration_out` är valfri. Om den saknas används senaste show-anropets `duration_out`, annars fallback `500` ms.

## Anpassa designen

All frontend-kod finns i:

```text
templates/overlay.html
```

Ändra CSS-variabler, layout, typsnitt, färger och animationer där.

## Testa

```bash
go test ./...
```
