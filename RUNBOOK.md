# Runbook

## Service commands

```bash
sudo systemctl status aurora-agent
sudo systemctl restart aurora-agent
sudo journalctl -u aurora-agent -f
```

## Health checks

1. Agent process alive
2. Libvirt reachable (`qemu+unix:///system`)
3. Backend stream reachable
4. Metrics timestamps tăng liên tục

## Incident: libvirt disconnect loop

- Kiểm tra libvirt daemon:

```bash
sudo systemctl status libvirtd
sudo journalctl -u libvirtd -n 100 --no-pager
```

- Kiểm tra URI:
  - `AURORA_LIBVIRT_URI`

## Incident: backend stream fail

- Kiểm tra network + firewall
- Kiểm tra token hết hạn
- Kiểm tra TLS files tồn tại và đúng quyền đọc

## Upgrade

```bash
cd vm-agent
go build -o aurora-agent ./cmd/agent
sudo install -m 0755 aurora-agent /usr/local/bin/aurora-agent
sudo systemctl restart aurora-agent
```

## Rollback

- Khôi phục binary version trước đó
- Restart service
- Theo dõi `journalctl -u aurora-agent -f`
