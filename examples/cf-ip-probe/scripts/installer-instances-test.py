"""Run the installer in a temporary tree using local packages and init stubs.
No host service is changed. Cover systemd, procd and OpenRC naming/isolation.
"""
import hashlib,io,os,subprocess,tarfile,tempfile
from pathlib import Path

for init in ['systemd','procd','openrc']:
 with tempfile.TemporaryDirectory(prefix='cf-probe-installer-') as tmp:
  root=Path(tmp);etc=root/'etc';run=root/'run';bin=root/'bin';fixtures=root/'fixtures';stubs=root/'stubs';sbin=root/'sbin'
  for p in [etc/'systemd/system',etc/'init.d',run,bin,fixtures,stubs,sbin]:p.mkdir(parents=True,exist_ok=True)
  script=Path(__file__).with_name('install.sh').read_text().replace('/etc/',str(etc)+'/').replace('/run/',str(run)+'/').replace('/sbin/',str(sbin)+'/')
  installer=fixtures/'install.sh';installer.write_text(script)
  pkg=fixtures/'cf-ip-probe-linux-amd64.tar.gz'
  with tarfile.open(pkg,'w:gz') as t:
   data=b'#!/bin/sh\necho cf-ip-probe 9.0.0\n';info=tarfile.TarInfo('cf-ip-probe');info.size=len(data);info.mode=0o755;t.addfile(info,io.BytesIO(data))
  (fixtures/'sha256sums.txt').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.name+'\n' for p in [pkg,installer]))
  curl=stubs/'curl';curl.write_text('''#!/usr/bin/env python3
import os,sys,shutil
from pathlib import Path
a=sys.argv[1:]
if '-o' not in a:
 print('[{"tag_name":"cf-ip-probe-v9.0.0"}]');sys.exit(0)
out=a[a.index('-o')+1];name=a[-1].rsplit('/',1)[-1]
shutil.copyfile(Path(os.environ['INSTALL_FIXTURES'])/name,out)
''');curl.chmod(0o755)
  for name,text in [('id','echo 0'),('uname','case "$1" in -s) echo Linux;; -m) echo x86_64;; esac'),('systemctl','printf "%s\\n" "$*" >> "$INSTALL_CALLS"'),('rc-service','exit 0'),('rc-update','exit 0')]:
   p=stubs/name;p.write_text('#!/bin/sh\n'+text+'\n');p.chmod(0o755)
  if init=='systemd':(run/'systemd/system').mkdir(parents=True)
  if init=='procd':
   (etc/'rc.common').write_text('exit 0\n');(sbin/'procd').write_text('#!/bin/sh\nexit 0\n');(sbin/'procd').chmod(0o755)
  if init=='openrc':
   (stubs/'openrc-run').write_text('#!/bin/sh\nexit 0\n');(stubs/'openrc-run').chmod(0o755)
  env=dict(os.environ,PATH=str(stubs)+':'+os.environ['PATH'],INSTALL_FIXTURES=str(fixtures),INSTALL_CALLS=str(root/'calls'))
  def call(*args):
   r=subprocess.run(['sh',str(installer),'--dir',str(bin),*args],env=env,capture_output=True,text=True)
   assert r.returncode==0,r.stdout+r.stderr
  for instance,port in [('alpha',80),('beta',8443),('',443)]:
   args=['--instance',instance] if instance else []
   call(*args,'--no-start','--api','https://panel.example/api/v1','--token','synthetic-token-'+(instance or 'default'),'--profile','fixture','--source',instance or 'default','--region','CN-31' if instance=='alpha' else 'CN-44','--host','cdn.example','--args',f'-scheme {"http" if port==80 else "https"} -port {port}')
   name='cf-ip-probe'+('-'+instance if instance else '')
   assert (bin/name).exists()
   assert f'-port {port}' in (etc/(name+'.env')).read_text()
   assert 'PROBE_REGION=' in (etc/(name+'.env')).read_text()
   service=etc/'systemd/system'/f'{name}.service' if init=='systemd' else etc/'init.d'/name
   assert str(etc/(name+'.env')) in service.read_text()
   if init=='procd':assert 'PROBE_REGION=' in service.read_text()
   updater=(bin/(name+'-update')).read_text()
   if instance:assert '--instance '+instance in updater
  before=(etc/'cf-ip-probe-beta.env').read_bytes()
  call('--instance','alpha','--uninstall')
  assert not (etc/'cf-ip-probe-alpha.env').exists()
  assert (etc/'cf-ip-probe-beta.env').read_bytes()==before
  assert (etc/'cf-ip-probe.env').exists()
  if init!='systemd':
   cron=(etc/'crontabs/root').read_text()
   assert str(bin/'cf-ip-probe-alpha-update') not in cron
   assert str(bin/'cf-ip-probe-beta-update') in cron and str(bin/'cf-ip-probe-update') in cron
  # Force an actual reinstall, rather than only the already-current fast path.
  call('--instance','beta','--update','--version','9.0.1','--no-start')
  assert (etc/'cf-ip-probe-beta.env').read_bytes()==before
  for bad in ['../../bad','update','alpha-update']:
   r=subprocess.run(['sh',str(installer),'--instance',bad],env=env,capture_output=True)
   assert r.returncode!=0
  print('PASS',init,'independent default/HTTP/HTTPS instances, update/uninstall isolation, reserved-name validation')
