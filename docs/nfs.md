# NFS `/mnt/dabing` (JODA → M2 + SPARK)

Orchestrátor (M2) a workery (SPARK) si předávají jen cesty, takže obě strany musí
vidět úložiště pod **stejnou cestou** `/mnt/dabing`. Nic z toho zatím není nastavené
(stav 2026-10-26: `/mnt/dabing` neexistuje, JODA nemá `/etc/exports`). Všechny kroky
potřebují sudo — dělá je uživatel.

Dokud NFS neběží, jde pipeline pustit s fallbackem `storage: upload`
(`DUB_SEPARATE_STORAGE=upload`): audio se nahraje na worker, výstupy se stáhnou zpět.

## 1. JODA (192.168.88.88) — export

`/pool` je ZFS s 7,2 TB volného místa (`/media` je na 96 %).

```bash
sudo zfs create -o compression=lz4 -o recordsize=1M pool/dabing   # → /pool/dabing
sudo mkdir -p /pool/dabing/{incoming,work,done,voices,series}
sudo chown -R oli:oli /pool/dabing
sudo apt install -y nfs-kernel-server
echo '/pool/dabing 192.168.88.0/24(rw,sync,no_subtree_check,all_squash,anonuid=1000,anongid=1000,insecure)' \
  | sudo tee -a /etc/exports
sudo exportfs -ra && sudo systemctl enable --now nfs-server
```

`all_squash` na uid/gid 1000 (`oli`) — M2 (uid 501), SPARK (`ol1n`) i kontejnery
workerů pak zapisují jako jeden vlastník. `insecure` je kvůli macOS klientovi.
Ověř `id -u oli` na JODA a případně uprav `anonuid`.

## 2. SPARK (192.168.88.66)

```bash
sudo apt install -y nfs-common
sudo mkdir -p /mnt/dabing
echo '192.168.88.88:/pool/dabing /mnt/dabing nfs4 rw,hard,noatime,rsize=1048576,wsize=1048576,_netdev 0 0' \
  | sudo tee -a /etc/fstab
sudo mount /mnt/dabing && touch /mnt/dabing/.spark-ok && ls -la /mnt/dabing
```

## 3. Mac Mini M2

Kořen systému je na macOS jen pro čtení, `/mnt` se musí vytvořit přes `synthetic.conf`
(vyžaduje restart) a mount přes autofs:

```bash
printf 'mnt\tSystem/Volumes/Data/mnt\n' | sudo tee -a /etc/synthetic.conf
sudo mkdir -p /System/Volumes/Data/mnt/dabing
# restart, pak:
# autofs mapu dát na skutečnou cestu (ne přes symlink /mnt)
echo '/System/Volumes/Data/mnt/dabing  -fstype=nfs,vers=4,resvport,rw,hard,nolocks,rsize=1048576,wsize=1048576  nfs://192.168.88.88/pool/dabing' \
  | sudo tee /etc/auto_dabing
echo '/-  auto_dabing  -nosuid' | sudo tee -a /etc/auto_master
sudo automount -cv
ls /mnt/dabing
```

## 4. Ověření

```bash
dub doctor        # na M2: fs=nfs, zapisovatelné, adresáře incoming/work/done/voices/series
ssh spark 'ls -la /mnt/dabing && cat /mnt/dabing/.spark-ok && echo ok'
```

Pokud bude NFS pro `-c:v copy` mux velkých MKV pomalé (DABING_PLAN §10), změřit
`dd if=/mnt/dabing/… of=/dev/null bs=1m` a případně mux dělat na lokálním disku M2.
