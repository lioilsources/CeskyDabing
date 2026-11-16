# Sdílené úložiště `/mnt/dabing`

Orchestrátor (M2) a workery (SPARK) si předávají jen cesty k souborům. Proto musí obě
strany vidět úložiště pod **stejnou cestou**, výchozí je `/mnt/dabing` (`DUB_ROOT`).
Kdyby to na jedné straně nešlo, worker dostane v configu `remote_root` a cesty se
přepisují — to je ale jen nouzovka.

Konkrétní příkazy pro export a mount (vyžadují sudo) má vlastník mimo toto repo.

## Požadavky

1. **Stejná cesta** na M2 i na SPARKu.
2. **Zápis z obou stran pod jedním vlastníkem.** Kontejnery workerů běží pod
   `DUB_UID:DUB_GID`, M2 pod svým uživatelem. Server má mapovat všechny klienty na
   jednoho vlastníka (u NFS `all_squash` + `anonuid/anongid`).
3. **Atomický `rename`** v rámci adresáře. Všechno se zapisuje jako `.jméno.tmp`
   a přejmenuje, takže nedokončený soubor nikdy nevypadá hotově.
4. **Podadresáře:** `incoming/ work/ done/ voices/ series/`.
5. **Kapacita:** jeden 22minutový díl zabere ve `work/` zhruba 1,5–2 GB (několik WAV
   po ~250 MB) plus vstupní a výstupní MKV. Série o 50 dílech jsou desítky až ~150 GB,
   pokud se `work/` nečistí.
6. **Propustnost:** stačí gigabit. Nejtěžší je mux (`-c:v copy`), kdy se přes síť čte
   a zapisuje celé video. Když bude pomalý, mux dělat do lokálního dočasného adresáře.

SQLite na sdíleném úložišti **není** — databáze je lokálně na M2 (`DUB_DB`).

## Bez sdíleného úložiště

Pipeline jede i bez něj: `DUB_SEPARATE_STORAGE=upload` (nebo `storage: upload`
v `dub.yaml`). Audio se nahraje na worker přes `POST /v1/files` a výstupy se stáhnou
zpět. Je to pomalejší, ale funkčně stejné.

## Ověření

```bash
bin/dub doctor   # úložiště: fs=nfs, zapisovatelné, žádné chybějící adresáře
```
