import tempfile,sqlite3,pathlib,subprocess,hashlib,json
binary=pathlib.Path(__file__).resolve().parent/'native-map-mac'
with tempfile.TemporaryDirectory() as d:
 p=pathlib.Path(d)/'tasks.db';db=sqlite3.connect(p);db.execute('CREATE TABLE TASKS(ID INTEGER,UID INTEGER,TYPE TEXT,STATE TEXT,HASH_V1 TEXT,TASK_ID TEXT)')
 db.executemany('INSERT INTO TASKS VALUES(?,?,?,?,?,?)',[(1,1000,'qbt','downloading','a'*40,'a'*40),(2,1001,'qbt','done','b'*40,'private-url?token=secret')]);db.commit();db.close();before=hashlib.sha256(p.read_bytes()).hexdigest()
 result=subprocess.run([str(binary),'1000',str(p)],text=True,capture_output=True,check=True);assert 'rows=1' in result.stdout and 'b'*40 not in result.stdout and 'secret' not in result.stdout;assert json.loads(result.stdout.splitlines()[1])['id']==1
 assert before==hashlib.sha256(p.read_bytes()).hexdigest()
 missing=pathlib.Path(d)/'missing.db';r=subprocess.run([str(binary),'1000',str(missing)],capture_output=True);assert r.returncode!=0 and not missing.exists()
 r=subprocess.run([str(binary),'1000 OR 1=1',str(p)],capture_output=True);assert r.returncode!=0
print('PASS read-only database hash unchanged; UID isolation; no-create and invalid UID rejected')
