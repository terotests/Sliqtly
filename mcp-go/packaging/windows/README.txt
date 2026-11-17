Sliqtly server for Windows
==========================

The Sliqtly MCP server with the editor and player built in, as a Windows
service. Assistants (Claude, Cursor, VS Code) make and change
presentations over MCP; people play and edit them in the browser.
Windows 10, Windows 11 and Windows Server 2016 or newer, 64-bit.

Install
-------

The installer (sliqtly-server_<version>_windows_amd64.msi): open it and
allow it to make changes. It installs the program in
C:\Program Files\Sliqtly, starts the Sliqtly service (at every start of
Windows from then on) and lets the port in through Windows Firewall on
domain and private networks. Silently:

  msiexec /i sliqtly-server_<version>_windows_amd64.msi /qn

From the .zip instead: put sliqtly-server.exe where it stays (for example
C:\Program Files\Sliqtly) and run, in a Command Prompt or PowerShell
opened as administrator:

  sliqtly-server service install

An upgrade is the same: the newer .msi, or the newer .exe in the same
place and "service install" again. Settings and decks stay.

Where things are
----------------

  C:\ProgramData\Sliqtly\sliqtly.env      the settings
  C:\ProgramData\Sliqtly\data             the decks (sliqtly.db, blobs.db)
  C:\ProgramData\Sliqtly\backup           the backups
  C:\ProgramData\Sliqtly\log\sliqtly.log  the log

Only administrators can open the folder. The service runs as an account
of its own, NT SERVICE\Sliqtly, which can read the settings and write
the decks, backups and log, and nothing else.

Using it
--------

The server listens on port 8080 of every network (SLIQTLY_LISTEN=network),
so every request needs the access token. The server makes it at its first
start: it is in C:\ProgramData\Sliqtly\data\access-token and in the log.

  Browser:   http://localhost:8080/?token=<token> once (it keeps a cookie)
  Assistant: http://localhost:8080/mcp with the header
             Authorization: Bearer <token>
             (or http://<this-computer>:8080/mcp from another computer)

For this computer only, set SLIQTLY_LISTEN=local in sliqtly.env.

Settings
--------

Edit C:\ProgramData\Sliqtly\sliqtly.env (open Notepad as administrator),
then, as administrator:

  sliqtly-server service restart

Each setting is explained in the file. The service itself:

  sliqtly-server service status
  sliqtly-server service stop
  sliqtly-server service start

Backups
-------

An incremental backup every 24 hours into C:\ProgramData\Sliqtly\backup
(best on another disk: SLIQTLY_BACKUP=D:\SliqtlyBackup). As administrator:

  sliqtly-server backup list   -repo C:\ProgramData\Sliqtly\backup
  sliqtly-server backup verify -repo C:\ProgramData\Sliqtly\backup
  sliqtly-server backup restore -repo C:\ProgramData\Sliqtly\backup -into D:\restored

Remove
------

Settings → Apps → Sliqtly server → Uninstall (or "sliqtly-server service
uninstall" for the .zip). The service and the firewall rule go;
C:\ProgramData\Sliqtly with the settings, decks and backups stays until
you delete it.
