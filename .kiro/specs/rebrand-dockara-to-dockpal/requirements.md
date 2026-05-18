# Requirements Document

## Introduction

Aplikasi yang sebelumnya bernama **Dockara** akan di-rebrand menjadi **Dockpal**. Rebrand ini bersifat menyeluruh: nama produk yang ditampilkan ke pengguna, nama biner CLI, nama unit systemd, path data filesystem, environment variables, label container Docker, namespace JavaScript front-end, module path Go, dan seluruh dokumentasi serta artefak rilis. Repository upstream berpindah dari `indatech/dockara` (URL lama yang dirujuk di installer) ke `sdldev/dockpal`.

Tujuan rebrand:

1. Mengganti seluruh string identitas brand dari "Dockara" / "dockara" / "DOCKARA" menjadi "Dockpal" / "dockpal" / "DOCKPAL" secara konsisten di kode produksi, dokumentasi, dan artefak rilis.
2. Memastikan instalasi baru (fresh install) Dockpal berhasil end-to-end menggunakan installer baru.
3. Memastikan instalasi Dockara yang sudah ada di mesin pengguna dapat di-upgrade ke Dockpal dengan migrasi data otomatis tanpa kehilangan database, secret JWT, repo Git yang sudah di-clone, atau konfigurasi Traefik.
4. Memastikan container yang sebelumnya dikelola oleh Dockara (memiliki label `dockara.*`) tetap dapat dikelola oleh Dockpal selama jendela deprecation, sehingga auto-recovery dan operasi compose tidak putus saat upgrade.
5. Mempertahankan kelulusan seluruh test suite (unit, property-based, integration) setelah rename.

Skenario non-tujuan: rebrand ini bukan untuk mengubah fitur, skema database, struktur API, atau perilaku runtime di luar yang ditentukan oleh rename identitas.

## Glossary

- **Dockara**: Nama brand lama produk. Akan dihapus dari semua surface kecuali jalur kompatibilitas yang didefinisikan eksplisit (label container legacy, deteksi instalasi lama oleh installer).
- **Dockpal**: Nama brand baru produk.
- **Brand_String**: Salah satu varian literal `Dockara`, `dockara`, atau `DOCKARA` yang muncul di source code, dokumentasi, atau artefak.
- **Codebase**: Seluruh file di repository kecuali path yang dikecualikan (`.git/`, `go.sum`, file biner ter-build, direktori spec `.kiro/`, `.auto-claude/`, dan artefak debugging `.playwright-mcp/`).
- **Production_Source**: Subset Codebase yang membentuk artefak yang dibangun dan dijalankan: file Go (`*.go`), file web (`web/**`), `installer.sh`, file unit systemd, `go.mod`, `.github/workflows/**`, `templates/**`.
- **Module_Path**: Nilai direktif `module` di `go.mod`.
- **Binary_Name**: Nama file biner yang dihasilkan oleh `go build` dan dipasang ke `/usr/local/bin/`.
- **Service_Unit**: Nama unit systemd yang dipasang di `/etc/systemd/system/`.
- **Data_Dir**: Direktori akar tempat database, secret, log, repo Git yang di-clone, dan file compose disimpan.
- **Legacy_Data_Dir**: `/opt/dockara/` — direktori data yang digunakan oleh instalasi Dockara sebelum rebrand.
- **New_Data_Dir**: `/opt/dockpal/` — direktori data yang akan digunakan oleh Dockpal.
- **Env_Var_Prefix**: Prefiks environment variable untuk konfigurasi runtime. Lama: `DOCKARA_`. Baru: `DOCKPAL_`.
- **Container_Label_Namespace**: Prefiks label Docker yang digunakan untuk menandai container yang dikelola. Lama: `dockara.*`. Baru: `dockpal.*`.
- **JS_Namespace**: Global object di browser yang menampung modul front-end. Lama: `window.Dockara`. Baru: `window.Dockpal`.
- **Auth_Storage_Key**: Nama key di `localStorage` browser untuk menyimpan token JWT. Lama: `dockara_token`. Baru: `dockpal_token`.
- **Installer**: Skrip `installer.sh` yang dijalankan oleh pengguna untuk memasang Dockpal sebagai layanan systemd.
- **Release_Workflow**: GitHub Actions workflow di `.github/workflows/release.yml` yang membangun biner dan mempublikasikan release.
- **Upgrade_Path**: Skenario di mana mesin sudah memiliki instalasi Dockara aktif dan installer baru dijalankan.
- **Fresh_Install_Path**: Skenario di mana mesin tidak memiliki instalasi Dockara sebelumnya.
- **Allowlist**: Daftar lokasi terbatas di mana Brand_String lama masih boleh muncul setelah rebrand selesai (lihat Requirement 12).

## Requirements

### Requirement 1: Rebrand Module Path dan Binary

**User Story:** Sebagai maintainer, saya ingin module path Go dan nama biner mencerminkan brand baru, sehingga import path dan artefak konsisten dengan repository baru.

#### Acceptance Criteria

1. THE Codebase SHALL set Module_Path di `go.mod` menjadi nilai literal `github.com/sdldev/dockpal` tanpa trailing slash dan tanpa whitespace tambahan.
2. THE Codebase SHALL mengganti seluruh import statement Go di semua file `.go` (termasuk `*_test.go`, `*_prop_test.go`, `*_property_test.go`) sehingga pencarian teks untuk `github.com/dockara/dockara` mengembalikan nol match pada `*.go` di repository.
3. WHEN perintah `go build -o dockpal .` dijalankan dari root repository, THE Codebase SHALL menyelesaikan kompilasi dengan exit code 0 dan menghasilkan file biner `dockpal` di root repository.
4. THE Codebase SHALL TIDAK lagi memiliki file biner `dockara` di root repository, sebagaimana dapat diverifikasi dengan `git ls-files dockara` mengembalikan output kosong.
5. WHEN perintah `go build ./...` dijalankan dari root repository, THE Codebase SHALL menyelesaikan kompilasi dengan exit code 0 tanpa pesan error yang berisi `cannot find module` atau `package ... is not in std`.
6. IF perintah `go build ./...` mengeluarkan pesan error yang berisi substring `github.com/dockara/dockara`, THEN THE Codebase SHALL dianggap belum memenuhi Requirement 1 dan diperlakukan sebagai gagal pada criterion 5.

### Requirement 2: Rebrand CLI Command dan Output

**User Story:** Sebagai pengguna CLI, saya ingin perintah dan output mencerminkan nama Dockpal, sehingga saya tidak melihat referensi ke nama lama.

#### Acceptance Criteria

1. WHEN pengguna menjalankan `dockpal` tanpa argumen, THE Dockpal_CLI SHALL menulis ke stdout teks yang mengandung substring `Dockpal` (case-sensitive) dan tidak mengandung substring `Dockara` (case-sensitive), serta menyelesaikan eksekusi dengan exit code 0.
2. WHEN pengguna menjalankan `dockpal version`, THE Dockpal_CLI SHALL menulis ke stdout sebuah baris dengan pola `Dockpal v<MAJOR>.<MINOR>.<PATCH>` di mana `<MAJOR>`, `<MINOR>`, dan `<PATCH>` adalah bilangan bulat non-negatif sesuai SemVer 2.0.0, tanpa substring `Dockara`, dan menyelesaikan eksekusi dengan exit code 0.
3. WHEN pengguna menjalankan `dockpal help`, THE Dockpal_CLI SHALL menulis ke stdout (a) banner yang mengandung substring `Dockpal`, (b) daftar subcommand yang mencakup `server`, `reset-password`, `version`, dan `help`, dan (c) deskripsi setiap subcommand yang mengandung substring `Dockpal` dan tidak mengandung substring `Dockara`, serta menyelesaikan eksekusi dengan exit code 0.
4. WHEN pengguna menjalankan `dockpal server`, THE Dockpal_Server SHALL menulis ke stdout atau stderr setidaknya satu baris log startup yang mengandung substring `Dockpal` dan tidak mengandung substring `Dockara` dalam waktu 10 detik sejak proses dimulai.
5. WHEN pengguna menjalankan `dockpal reset-password`, THE Dockpal_CLI SHALL membaca Data_Dir yang sama dengan yang digunakan oleh `dockpal server` (lihat Requirement 3 dan 4) dan menulis ke stdout sebuah pesan sukses yang mengandung substring `Dockpal`, serta menyelesaikan eksekusi dengan exit code 0.
6. IF Data_Dir tidak dapat diakses atau database admin tidak ditemukan saat `dockpal reset-password` dijalankan, THEN THE Dockpal_CLI SHALL menulis ke stderr pesan error yang mengindikasikan masalah akses Data_Dir atau database, tidak memodifikasi file apapun, dan menyelesaikan eksekusi dengan exit code non-zero.

### Requirement 3: Rebrand Environment Variables (Breaking Change)

**User Story:** Sebagai operator, saya ingin variabel lingkungan menggunakan prefiks `DOCKPAL_`, sehingga konfigurasi konsisten dengan brand baru.

#### Acceptance Criteria

1. WHEN Dockpal_Server memulai proses startup, THE Dockpal_Server SHALL membaca path Data_Dir dari environment variable `DOCKPAL_DATA_DIR` sebelum menerima request apapun.
2. WHEN Dockpal_Server memulai proses startup, THE Dockpal_Server SHALL membaca path database dari environment variable `DOCKPAL_DB_PATH` sebelum membuka koneksi database.
3. WHEN Dockpal_Server memulai proses startup, THE Dockpal_Server SHALL membaca path log dari environment variable `DOCKPAL_LOG_PATH` sebelum menulis entri log pertama.
4. WHEN Installer dieksekusi, THE Installer SHALL membaca versi target dari environment variable `DOCKPAL_VERSION` sebelum melakukan unduhan artefak.
5. IF environment variable `DOCKPAL_DATA_DIR` tidak diset atau bernilai string kosong, THEN THE Dockpal_Server SHALL menggunakan nilai default `/opt/dockpal/data`.
6. IF environment variable `DOCKPAL_DB_PATH` tidak diset atau bernilai string kosong, THEN THE Dockpal_Server SHALL menggunakan nilai default `<DATA_DIR>/dockpal.db`, di mana `<DATA_DIR>` merujuk pada nilai efektif `DOCKPAL_DATA_DIR` setelah penerapan default.
7. IF environment variable `DOCKPAL_LOG_PATH` tidak diset atau bernilai string kosong, THEN THE Dockpal_Server SHALL menggunakan nilai default `<DATA_DIR>/dockpal.log`, di mana `<DATA_DIR>` merujuk pada nilai efektif `DOCKPAL_DATA_DIR` setelah penerapan default.
8. WHILE Dockpal_Server berjalan, THE Dockpal_Server SHALL mengabaikan seluruh environment variable berprefiks `DOCKARA_` sehingga nilai konfigurasi efektif tidak dipengaruhi oleh keberadaan variabel `DOCKARA_*` (breaking change — migrasi env var dilakukan oleh Installer pada Upgrade_Path, lihat Requirement 9).
9. IF environment variable `DOCKPAL_DATA_DIR`, `DOCKPAL_DB_PATH`, atau `DOCKPAL_LOG_PATH` berisi nilai bukan absolute path (tidak diawali karakter `/`), THEN THE Dockpal_Server SHALL menghentikan proses startup dengan exit code non-zero dan mengeluarkan pesan error yang mengindikasikan nama variabel dan alasan path tidak valid.
10. IF environment variable `DOCKPAL_VERSION` tidak diset atau bernilai string kosong saat Installer dieksekusi, THEN THE Installer SHALL menggunakan nilai default `latest` sehingga eksekusi Installer tetap berhasil pada Fresh_Install_Path.

### Requirement 4: Rebrand Path Filesystem

**User Story:** Sebagai operator, saya ingin Dockpal menyimpan datanya di `/opt/dockpal/`, sehingga path mencerminkan brand baru.

#### Acceptance Criteria

1. WHEN Dockpal_Server memulai proses startup tanpa override Data_Dir melalui environment variable `DOCKPAL_DATA_DIR`, THE Dockpal_Server SHALL menggunakan `/opt/dockpal/data` sebagai nilai efektif Data_Dir.
2. THE Dockpal_Server SHALL menyimpan secret JWT default di file `<DATA_DIR>/.secret` dengan permission `0600`.
3. THE Dockpal_Server SHALL menyimpan database BBolt default di file `<DATA_DIR>/dockpal.db`.
4. THE Dockpal_Server SHALL menulis log default ke file `<DATA_DIR>/dockpal.log`.
5. THE Dockpal_Server SHALL meng-clone repository Git deploy ke direktori `/opt/dockpal/repos/<repoIdentifier>/` di mana `<repoIdentifier>` unik per repo.
6. THE Dockpal_Server SHALL menulis file compose yang dihasilkan ke direktori `/opt/dockpal/compose/<projectName>/` di mana `<projectName>` unik per project.
7. THE Dockpal_Server SHALL membaca konfigurasi dinamis Traefik dari `/opt/dockpal/traefik/dynamic.yml` jika file tersebut ada dan dapat dibaca.
8. IF file `templates/templates.json` lokal tidak tersedia atau tidak dapat dibaca saat Dockpal_Server membutuhkan templates, THEN THE Dockpal_Server SHALL membaca templates fallback dari `/opt/dockpal/templates.json`.
9. WHEN Dockpal_Server memulai proses startup, THE Dockpal_Server SHALL membuat direktori `<DATA_DIR>`, `/opt/dockpal/repos/`, dan `/opt/dockpal/compose/` dengan permission `0750` jika belum ada.
10. IF Dockpal_Server gagal membuat atau menulis ke salah satu path wajib (`<DATA_DIR>`, `<DATA_DIR>/.secret`, `<DATA_DIR>/dockpal.db`, `<DATA_DIR>/dockpal.log`) karena permission denied atau disk error, THEN THE Dockpal_Server SHALL menghentikan proses startup dengan exit code non-zero dan menulis ke stderr pesan error yang mengindikasikan path dan alasan kegagalan.
11. IF file `/opt/dockpal/traefik/dynamic.yml` tidak ada, THEN THE Dockpal_Server SHALL melanjutkan startup tanpa membatalkan layanan, dengan menulis sebuah baris log peringatan yang menyebut path tersebut.

### Requirement 5: Rebrand Container Labels dengan Backward Compat Read

**User Story:** Sebagai operator yang upgrade dari Dockara, saya ingin container yang sudah saya deploy tetap dikelola Dockpal pasca-upgrade, sehingga auto-recovery dan operasi compose tidak putus.

#### Acceptance Criteria

1. WHEN Dockpal_Server berhasil membuat container melalui flow compose, THE Dockpal_Server SHALL menulis label `dockpal.managed=true`, `dockpal.project=<projectName>`, `dockpal.compose=<composePath>`, dan `dockpal.service=<svcName>` di mana setiap nilai placeholder sama persis dengan parameter input flow compose terkait.
2. WHEN Dockpal_Server berhasil membuat container Cloudflare tunnel terkelola, THE Dockpal_Server SHALL menulis label `dockpal.managed=true` dan `dockpal.tunnel=true`.
3. WHEN pengguna men-deploy template dengan opsi auto-recover di-enable dan deploy berhasil, THE Dockpal_Server SHALL menyisipkan label `dockpal.auto-recover=true` ke setiap container service yang dihasilkan oleh compose.
4. WHEN HealthMonitor melakukan scan auto-recover, THE Dockpal_Server SHALL memperlakukan sebuah container sebagai auto-recover-eligible jika dan hanya jika container memiliki label `dockpal.auto-recover=true` ATAU label `dockara.auto-recover=true`, dan setiap container hanya dievaluasi sekali per scan tanpa duplikasi.
5. WHEN Dockpal_Server melakukan operasi `StopCompose` atau `RemoveCompose` untuk `<projectName>` tertentu, THE Dockpal_Server SHALL mengaplikasikan operasi tersebut pada gabungan unik container yang memiliki label `dockpal.project=<projectName>` ATAU label `dockara.project=<projectName>`.
6. WHEN Dockpal_Server membuat container Cloudflare tunnel baru, THE Dockpal_Server SHALL menamai container tersebut `dockpal-cloudflared`.
7. WHEN Installer berjalan pada Upgrade_Path dan menemukan container Docker bernama `dockara-cloudflared`, THE Installer SHALL menjalankan salah satu jalur berikut sebelum men-start `dockpal.service`: (a) me-rename container tersebut menjadi `dockpal-cloudflared` dengan mempertahankan tunnel token yang sama, ATAU (b) menghentikan dan menghapus container lama lalu membuat container baru bernama `dockpal-cloudflared` yang menggunakan tunnel token yang sama yang dapat diverifikasi melalui inspeksi konfigurasi container.
8. IF langkah rename atau replace container `dockara-cloudflared` pada criterion 7 gagal, THEN THE Installer SHALL menghentikan upgrade dengan exit code non-zero, mempertahankan container lama `dockara-cloudflared` dalam state semula, tidak men-start `dockpal.service`, dan menulis pesan error yang mengindikasikan kegagalan migrasi tunnel beserta penyebabnya.
9. WHILE container Cloudflare tunnel lama bernama `dockara-cloudflared` masih ada karena migrasi belum dijalankan, THE Dockpal_Server SHALL memperlakukannya sebagai tunnel terkelola: container tersebut muncul di daftar tunnel yang dikembalikan API tunnel, operasi stop/start/restart pada tunnel tersebut diizinkan, dan container masuk ke scan auto-recover jika berlabel `dockara.auto-recover=true` (sesuai criterion 4).

### Requirement 6: Rebrand Web UI dan Front-End Namespace

**User Story:** Sebagai pengguna web UI, saya ingin antarmuka menampilkan nama Dockpal, sehingga brand konsisten secara visual dan internal.

#### Acceptance Criteria

1. THE Web_UI SHALL menampilkan teks `Dockpal` di tag `<title>` halaman utama, dan tag `<title>` SHALL TIDAK mengandung substring `Dockara`.
2. THE Web_UI SHALL menampilkan teks `Dockpal` sebagai heading pada halaman login, dan heading tersebut SHALL TIDAK mengandung substring `Dockara`.
3. THE Web_UI SHALL menampilkan teks `Dockpal` sebagai brand pada sidebar, dan brand sidebar SHALL TIDAK mengandung substring `Dockara`.
4. THE Web_UI SHALL mengganti seluruh referensi global `window.Dockara` di file JavaScript front-end menjadi `window.Dockpal`, sehingga pencarian teks pola `window.Dockara` di `web/**` mengembalikan nol match.
5. THE Web_UI SHALL mengganti nama fungsi entry point Alpine `dockaraApp()` menjadi `dockpalApp()`, sehingga pencarian teks pola `dockaraApp` di `web/**` mengembalikan nol match.
6. THE Web_UI SHALL memperbarui atribut `x-data` di `web/index.html` agar memanggil `dockpalApp()` sebagai entry point Alpine.
7. WHEN Web_UI menulis token JWT ke `localStorage` browser setelah login sukses, THE Web_UI SHALL menggunakan key `dockpal_token`.
8. WHEN Web_UI dimuat di browser yang `localStorage`-nya hanya berisi key `dockara_token` dari sesi Dockara sebelumnya, THE Web_UI SHALL memperlakukan sesi sebagai logout dan menampilkan halaman login (key `dockara_token` tidak dibaca; tidak ada migrasi token sisi browser).
9. THE Web_UI SHALL mengganti seluruh teks copy yang berisi `Dockara` di file HTML (mis. `template-config.html` "Dockara will automatically restart…") menjadi `Dockpal`, sehingga pencarian case-insensitive untuk pola `dockara` di `web/**/*.html` mengembalikan nol match.

### Requirement 7: Rebrand Service Unit dan Artefak Repo

**User Story:** Sebagai operator, saya ingin unit systemd dan file repository menggunakan nama baru, sehingga manajemen layanan dan rilis konsisten.

#### Acceptance Criteria

1. THE Codebase SHALL berisi tepat satu file unit systemd bernama `dockpal.service` di root repository.
2. THE Codebase SHALL TIDAK berisi file `dockara.service` di root repository maupun di subdirektori manapun.
3. THE File `dockpal.service` SHALL berisi baris `Description=Dockpal — Docker Management Platform` sama persis.
4. THE File `dockpal.service` SHALL berisi baris `WorkingDirectory=/opt/dockpal` sama persis.
5. THE File `dockpal.service` SHALL berisi baris `ExecStart=/usr/local/bin/dockpal server` sama persis.
6. THE File `dockpal.service` SHALL TIDAK mengandung substring `dockara` (case-insensitive).
7. WHEN Release_Workflow dieksekusi oleh event pemicu rilis (tag `v*`), THE Release_Workflow SHALL menghasilkan tepat tiga artefak biner bernama persis `dockpal-linux-amd64`, `dockpal-linux-arm64`, dan `dockpal-linux-armv7`.
8. WHEN Release_Workflow telah selesai menghasilkan artefak, THE Release_Workflow SHALL meng-upload artefak yang cocok dengan glob `dockpal-linux-*` ke GitHub Release terkait.
9. IF Release_Workflow tidak dapat menghasilkan ketiga artefak yang ditentukan pada criterion 7, THEN THE Release_Workflow SHALL menghentikan eksekusi dengan status failed dan tidak mempublikasikan GitHub Release parsial.

### Requirement 8: Rebrand Installer untuk Fresh Install

**User Story:** Sebagai pengguna baru, saya ingin menjalankan satu perintah curl untuk memasang Dockpal di mesin bersih, sehingga onboarding tetap mudah.

#### Acceptance Criteria

1. THE Installer SHALL men-download biner dari URL yang berbentuk `https://github.com/sdldev/dockpal/releases/<version_path>/dockpal-linux-<arch>` dengan timeout per percobaan maksimum 60 detik dan maksimum 3 kali percobaan ulang sebelum dianggap gagal.
2. THE Installer SHALL memasang biner ke `/usr/local/bin/dockpal` dengan permission executable (mode `0755`) dan ownership `root:root`.
3. THE Installer SHALL membuat direktori `/opt/dockpal/` beserta subdirektori `data`, `logs`, dan `repos` jika belum ada, tanpa menghapus atau menimpa isi yang sudah ada di dalamnya.
4. THE Installer SHALL menulis file unit systemd `/etc/systemd/system/dockpal.service` dengan `Description=Dockpal — Docker Management Platform`, `WorkingDirectory=/opt/dockpal`, dan `ExecStart=/usr/local/bin/dockpal server`.
5. THE Installer SHALL menjalankan `systemctl daemon-reload`, `systemctl enable dockpal`, dan `systemctl start dockpal` secara berurutan setelah unit file ditulis.
6. WHEN Installer berjalan di mesin tanpa instalasi Dockara sebelumnya dan tanpa instalasi Dockpal sebelumnya, THE Installer SHALL menyelesaikan instalasi dengan exit code 0 dan layanan `dockpal.service` SHALL berada dalam status `active (running)` dalam waktu maksimum 30 detik setelah `systemctl start dockpal` dijalankan.
7. WHEN Installer berjalan di mesin yang sudah memiliki instalasi Dockpal sebelumnya (deteksi: unit `dockpal.service` ada atau biner `/usr/local/bin/dockpal` ada), THE Installer SHALL memperlakukan eksekusi tersebut sebagai upgrade in-place: stop `dockpal.service`, ganti biner di `/usr/local/bin/dockpal` dengan versi yang baru di-download, perbarui unit file jika berbeda, lalu jalankan `systemctl daemon-reload` dan `systemctl start dockpal`, dengan tetap mempertahankan isi `/opt/dockpal/` (termasuk subdirektori `data`, `logs`, dan `repos`) tanpa modifikasi.
8. THE Installer SHALL mendeteksi arsitektur CPU host melalui `uname -m` dan memetakan ke salah satu artefak yang didukung: `x86_64` ke `amd64`, `aarch64` ke `arm64`, dan `armv7l` ke `armv7`.
9. IF download biner gagal setelah jumlah percobaan maksimum tercapai, THEN THE Installer SHALL menghentikan instalasi dengan exit code non-zero dan menampilkan pesan error yang menunjukkan kegagalan unduhan, tanpa memodifikasi biner di `/usr/local/bin/dockpal`, unit file `/etc/systemd/system/dockpal.service`, atau isi `/opt/dockpal/` yang sudah ada.
10. IF arsitektur CPU host yang terdeteksi tidak termasuk dalam `amd64`, `arm64`, atau `armv7`, THEN THE Installer SHALL menghentikan instalasi dengan exit code non-zero dan menampilkan pesan error yang menunjukkan arsitektur tidak didukung, sebelum melakukan unduhan biner atau modifikasi sistem apa pun.
11. IF Installer dijalankan tanpa hak akses root, THEN THE Installer SHALL menghentikan eksekusi dengan exit code non-zero dan menampilkan pesan error yang menunjukkan kebutuhan hak akses root, sebelum melakukan modifikasi pada `/usr/local/bin/`, `/opt/dockpal/`, atau `/etc/systemd/system/`.

### Requirement 9: Upgrade Path dari Instalasi Dockara yang Ada

**User Story:** Sebagai pengguna Dockara existing, saya ingin menjalankan installer baru dan otomatis ter-migrasi ke Dockpal, sehingga saya tidak kehilangan data atau konfigurasi.

#### Acceptance Criteria

1. WHEN Installer mendeteksi unit `dockara.service` aktif, THE Installer SHALL menjalankan `systemctl stop dockara` dengan timeout maksimum 30 detik sebelum melakukan migrasi.
2. IF perintah `systemctl stop dockara` tidak menyelesaikan stop dalam 30 detik atau mengembalikan exit code non-zero, THEN THE Installer SHALL menghentikan upgrade dengan exit code non-zero dan menulis pesan error yang mengindikasikan kegagalan stop layanan.
3. WHEN Installer mendeteksi direktori `/opt/dockara/` ada, THE Installer SHALL memindahkan isinya ke `/opt/dockpal/` sehingga file berikut menjadi tersedia di tujuan dengan ownership (uid/gid) dan mode permission yang sama dengan sumber: `/opt/dockpal/data/.secret`, `/opt/dockpal/data/<db file>`, `/opt/dockpal/repos/`, `/opt/dockpal/compose/`, dan `/opt/dockpal/traefik/`.
4. WHEN Installer memindahkan database, THE Installer SHALL me-rename file dari `dockara.db` menjadi `dockpal.db` di lokasi tujuan.
5. WHEN Installer memindahkan log dan file `dockara.log` ada di sumber, THE Installer SHALL me-rename file menjadi `dockpal.log` di lokasi tujuan; jika file `dockara.log` tidak ada, Installer SHALL melanjutkan tanpa error.
6. WHEN migrasi data selesai dan biner lama `/usr/local/bin/dockara` ada, THE Installer SHALL menghapus biner tersebut; jika tidak ada, Installer SHALL melanjutkan tanpa error.
7. WHEN migrasi data selesai dan unit `dockara.service` masih terdaftar di systemd, THE Installer SHALL menjalankan `systemctl disable dockara`, menghapus file `/etc/systemd/system/dockara.service`, dan menjalankan `systemctl daemon-reload` secara berurutan; setiap langkah berikutnya dijalankan hanya jika langkah sebelumnya berhasil dengan exit code 0, dan kegagalan dilaporkan melalui exit code non-zero pada Installer.
8. WHEN Installer telah menjalankan migrasi dan men-start `dockpal.service`, THE Dockpal_Server SHALL menerima request login dengan kredensial admin yang sama dengan yang berlaku di Dockara sebelum migrasi dan memberikan response auth sukses (exit code login sukses ekuivalen dengan login pada Dockara pra-migrasi).
9. IF migrasi gagal di langkah pemindahan direktori karena `/opt/dockpal/` sudah berisi data konflik (didefinisikan sebagai: file dengan nama yang sama yang tipenya berbeda — file vs direktori — atau file regular dengan konten berbeda dari sumber yang tidak dapat disatukan secara aman), THEN THE Installer SHALL berhenti dengan exit code non-zero dan menulis pesan error yang menyebut path konflik. (Catatan keputusan: Installer boleh meninggalkan hasil cleanup parsial dari langkah-langkah sebelumnya — mis. layanan `dockara.service` sudah di-stop — selama eksekusi tetap diakhiri dengan exit code non-zero.)
10. WHEN Installer selesai pada Upgrade_Path, THE Dockpal_Server SHALL menemukan dan terus mengelola container Docker yang sebelumnya memiliki label `dockara.*` (sesuai Requirement 5).

### Requirement 10: Update Dokumentasi README

**User Story:** Sebagai calon pengguna, saya ingin README mencerminkan brand Dockpal dan repository baru, sehingga instruksi yang saya ikuti benar.

#### Acceptance Criteria

1. THE README SHALL menggunakan judul level pertama (heading H1) yang sama persis dengan teks `Dockpal` (atau `🐳 Dockpal`).
2. THE README SHALL TIDAK mengandung substring `Dockara` (case-insensitive) di luar bagian bertajuk `## Upgrading from Dockara`.
3. THE README SHALL menampilkan perintah quick install yang merujuk URL `https://raw.githubusercontent.com/sdldev/dockpal/main/installer.sh`, dan tidak menampilkan URL yang merujuk path `dockara` atau `indatech/dockara`.
4. THE README SHALL menampilkan instruksi `git clone https://github.com/sdldev/dockpal.git`, dan tidak menampilkan instruksi `git clone` ke repository `dockara`.
5. THE README SHALL mendokumentasikan ketiga environment variables `DOCKPAL_DATA_DIR`, `DOCKPAL_DB_PATH`, dan `DOCKPAL_LOG_PATH` dengan nilai default `/opt/dockpal/data`, `<DATA_DIR>/dockpal.db`, dan `<DATA_DIR>/dockpal.log` masing-masing, beserta deskripsi singkat yang menjelaskan fungsi tiap variabel.
6. THE README SHALL mendokumentasikan label auto-recover sebagai `dockpal.auto-recover=true`, dan tidak mendokumentasikan `dockara.auto-recover` sebagai label yang harus diset oleh pengguna baru.
7. THE README SHALL berisi bagian dengan heading `## Upgrading from Dockara` yang menjelaskan ketiga hal berikut: (a) installer melakukan migrasi otomatis dari `/opt/dockara/` ke `/opt/dockpal/`, (b) installer mengganti unit systemd dari `dockara.service` ke `dockpal.service`, dan (c) installer menghapus biner lama `/usr/local/bin/dockara`.
8. THE README SHALL berisi bagian project structure yang menggunakan nama direktori akar `dockpal/`, dan tidak menampilkan `dockara/` sebagai nama direktori akar.
9. WHEN pencarian case-insensitive untuk pola `dockara` dijalankan terhadap README, THE README SHALL hanya mengembalikan match yang berada di dalam bagian `## Upgrading from Dockara` (sesuai criterion 7), sehingga seluruh referensi brand lama terisolasi pada bagian migrasi tersebut.

### Requirement 11: Pelestarian Test Suite

**User Story:** Sebagai maintainer, saya ingin seluruh test (unit dan property-based) tetap lulus setelah rebrand, sehingga tidak ada regresi yang masuk.

#### Acceptance Criteria

1. THE Codebase SHALL memperbarui seluruh import path di file test (`*_test.go`, `*_prop_test.go`, `*_property_test.go`) sehingga merujuk `github.com/sdldev/dockpal/...` alih-alih `github.com/dockara/dockara/...`, dan pencarian teks `github.com/dockara/dockara` di seluruh file test mengembalikan nol match.
2. WHEN `go test ./...` dijalankan dari root repository tanpa flag tambahan, THE Codebase SHALL menyelesaikan dengan exit code 0 dan output stdout/stderr SHALL TIDAK mengandung baris yang dimulai dengan `--- FAIL` atau `FAIL`.
3. THE Codebase SHALL mempertahankan, untuk setiap file `*_prop_test.go` dan `*_property_test.go`, jumlah test function (top-level `func TestXxx`), jumlah `t.Run` sub-test, dan jumlah iterasi property-based test setidaknya sama dengan nilai pada commit pra-rebrand (commit yang menjadi basis branch rebrand).
4. WHEN `go vet ./...` dijalankan dari root repository, THE Codebase SHALL menyelesaikan dengan exit code 0 dan tidak menulis baris peringatan atau error apapun ke stdout maupun stderr.
5. IF rebrand mengubah perilaku runtime suatu fungsi (mis. nama label container yang ditulis), THEN THE Codebase SHALL memperbarui assertion test terkait sehingga nilai harapan mencerminkan perilaku baru, dan test tersebut SHALL lulus saat `go test ./...` dijalankan (criterion 2).
6. THE Codebase SHALL memperbarui seluruh string literal `dockara` (case-insensitive) di test fixtures dan assertion yang dimaksudkan untuk perilaku Dockpal baru menjadi `dockpal`, kecuali untuk test yang secara eksplisit menguji jalur backward-compat baca label/path lama (Requirement 5 dan 9), yang SHALL ditandai dengan komentar `// LEGACY-DOCKARA: <alasan>` (sesuai Requirement 12).

### Requirement 12: Tidak Ada Sisa Brand Lama di Production_Source

**User Story:** Sebagai reviewer, saya ingin memverifikasi bahwa rebrand tuntas dengan satu pencarian, sehingga saya yakin tidak ada string "Dockara" yang tertinggal di kode produksi.

#### Acceptance Criteria

1. WHEN pencarian case-insensitive menggunakan regex `(?i)dockara` dijalankan terhadap Production_Source (sebagaimana didefinisikan di Glossary), THE Codebase SHALL hanya mengembalikan match yang berada di Allowlist berikut:
   - String literal di `installer.sh` yang mereferensikan `dockara.service`, `/opt/dockara`, atau biner `/usr/local/bin/dockara` semata-mata untuk keperluan deteksi dan migrasi instalasi lama (Requirement 9).
   - String literal di kode Go pada modul HealthMonitor (`internal/docker/recovery.go`) dan operasi compose (`internal/docker/compose.go`) yang membaca label `dockara.*` semata-mata untuk keperluan backward-compat baca (Requirement 5).
   - Komentar yang secara eksplisit menjelaskan jalur backward-compat di atas dan menyebut bahwa perilaku tersebut akan dihapus pada rilis berikutnya.
2. THE Codebase SHALL menambahkan komentar penanda di setiap lokasi Allowlist berbentuk `// LEGACY-DOCKARA: <alasan>` (untuk Go) atau `# LEGACY-DOCKARA: <alasan>` (untuk shell), di mana `<alasan>` adalah teks 1–200 karakter yang menjelaskan justifikasi backward-compat. Komentar penanda SHALL ditempatkan di baris yang sama dengan string literal atau pada baris tepat di atasnya.
3. THE Allowlist SHALL TIDAK mencakup file di `web/**`, `README.md`, `go.mod`, `go.sum`, file unit systemd di repo, file workflow rilis di `.github/workflows/**`, atau file Go di luar dua modul yang disebut di criterion 1.
4. WHEN pencarian case-insensitive menggunakan regex `(?i)dockara` dijalankan terhadap `web/**`, THE Codebase SHALL mengembalikan nol match tanpa pengecualian Allowlist apa pun.
5. WHEN pencarian case-sensitive menggunakan regex `DOCKARA_` dijalankan terhadap Production_Source, THE Codebase SHALL mengembalikan nol match.
6. IF pencarian pada criterion 1, 4, atau 5 mengembalikan match yang berada di luar Allowlist atau tidak diiringi komentar penanda yang valid (sesuai criterion 2), THEN THE Codebase SHALL dianggap belum memenuhi Requirement 12 dan rebrand SHALL diperlakukan sebagai belum selesai.

### Requirement 13: Versioning Rilis Rebrand

**User Story:** Sebagai pengguna, saya ingin rilis rebrand memiliki nomor versi yang jelas, sehingga saya tahu kapan transisi terjadi.

#### Acceptance Criteria

1. THE Codebase SHALL menetapkan konstanta `version` di `main.go` ke string literal `0.2.0` pada rilis rebrand pertama.
2. WHEN tag Git `v0.2.0` di-push ke remote `origin`, THE Release_Workflow SHALL berjalan hingga selesai dengan status `success` dan mempublikasikan satu GitHub Release yang berisi tepat tiga artefak biner bernama `dockpal-linux-amd64`, `dockpal-linux-arm64`, dan `dockpal-linux-armv7`.
3. IF Release_Workflow yang dipicu oleh tag `v0.2.0` selesai dengan status selain `success`, THEN THE Maintainer SHALL menghapus tag `v0.2.0` dari remote, memperbaiki workflow, lalu mempush ulang tag, sehingga tag `v0.2.0` di remote selalu berkorespondensi dengan eksekusi Release_Workflow yang berstatus `success`.
4. THE GitHub_Release_Notes untuk `v0.2.0` SHALL berisi pernyataan eksplisit yang menyebut bahwa rilis ini adalah rebrand dari Dockara dan SHALL memuat referensi ke prosedur upgrade pada Requirement 9 (mis. tautan ke bagian "Upgrading from Dockara" di README).

### Requirement 14: Konfigurasi Git Remote

**User Story:** Sebagai maintainer, saya ingin local clone menunjuk ke repository baru, sehingga `git push` masuk ke `sdldev/dockpal`.

#### Acceptance Criteria

1. THE Local_Repository SHALL memiliki tepat satu remote bernama `origin` dengan URL berformat `https://github.com/sdldev/dockpal.git` (HTTPS) atau `git@github.com:sdldev/dockpal.git` (SSH), yang dapat diverifikasi melalui output perintah `git remote get-url origin`.
2. WHEN maintainer menjalankan `git push -u origin <branch>` dengan kredensial Git yang valid, THE Local_Repository SHALL menyelesaikan transfer commit ke repository `sdldev/dockpal` dengan perintah `git push` mengembalikan exit code 0 dan branch lokal `<branch>` terkonfigurasi melacak (`upstream`) `origin/<branch>`.
3. IF Local_Repository memiliki remote yang URL-nya menunjuk ke repository dengan substring `dockara` (referensi sebelum rebrand), THEN THE Local_Repository SHALL menghapus atau mengubah URL remote tersebut sehingga tidak ada remote yang mengarah ke repository lama.
