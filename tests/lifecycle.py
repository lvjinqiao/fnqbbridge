# coding: utf-8
import tempfile,subprocess,os,pathlib
root=pathlib.Path(__file__).resolve().parents[1];pkg=root/'packaging'
with tempfile.TemporaryDirectory() as t:
 p=pathlib.Path(t)/'appvar';p.mkdir();media=pathlib.Path(t)/'media';media.mkdir();(media/'movie.mkv').write_text('protected')
 env=dict(os.environ,TRIM_PKGVAR=str(p),TRIM_APPDEST=str(pkg/'app'),wizard_keep_data='keep',wizard_bridge_password='newPassword123')
 original='{"password":"oldPassword123","port":8888}'
 (p/'config.json').write_text(original);(p/'bridge.log').write_text('log');(p/'.fnqbbridge-owned').touch()
 subprocess.run(['bash',str(pkg/'cmd/install_callback')],env=env,check=True)
 assert (p/'config.json').read_text()==original
 subprocess.run(['bash',str(pkg/'cmd/upgrade_callback')],env=env,check=True);assert (p/'config.json').read_text()==original
 subprocess.run(['bash',str(pkg/'cmd/uninstall_init')],env=env,check=True)
 subprocess.run(['bash',str(pkg/'cmd/uninstall_callback')],env=env,check=True);assert (p/'config.json').exists()
 env['wizard_keep_data']='remove';subprocess.run(['bash',str(pkg/'cmd/uninstall_callback')],env=env,check=True)
 assert not (p/'config.json').exists() and not (p/'bridge.log').exists();assert (media/'movie.mkv').read_text()=='protected'
 (p/'config.json').write_text('unmarked');subprocess.run(['bash',str(pkg/'cmd/uninstall_callback')],env=env,check=True);assert (p/'config.json').exists()
print('PASS install/upgrade preserve config; uninstall keep/remove; unrelated media and unmarked directories protected')
