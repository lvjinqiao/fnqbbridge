/* Fixed read-only SQL; no deletion/API methods or user-supplied SQL. */
#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <limits.h>
typedef struct sqlite3 sqlite3; typedef struct sqlite3_stmt sqlite3_stmt;
static int (*open_db)(const char*,sqlite3**,int,const char*);
static int (*close_db)(sqlite3*);static int (*prepare)(sqlite3*,const char*,int,sqlite3_stmt**,const char**);static int (*step)(sqlite3_stmt*);static int (*finalize)(sqlite3_stmt*);static int (*bind_int)(sqlite3_stmt*,int,int);static long long (*col_int)(sqlite3_stmt*,int);static const unsigned char*(*col_text)(sqlite3_stmt*,int);static const char*(*err_msg)(sqlite3*);static int(*readonly)(sqlite3*,const char*);
#define LOAD(target,name) do{*(void**)(&target)=dlsym(lib,name);if(!target){fprintf(stderr,"missing SQLite function %s\n",name);return 1;}}while(0)
static int hash_ok(const char*s){size_t n=strlen(s);if(n!=40&&n!=64)return 0;for(size_t i=0;i<n;i++)if(!((s[i]>='0'&&s[i]<='9')||(s[i]>='a'&&s[i]<='f')||(s[i]>='A'&&s[i]<='F')))return 0;return 1;}
static void quoted(const char*s){putchar('"');for(const unsigned char*p=(const unsigned char*)s;*p;p++){if(*p=='"'||*p=='\\'){putchar('\\');putchar(*p);}else if(*p<32)printf("\\u%04x",*p);else putchar(*p);}putchar('"');}
static const char*text(sqlite3_stmt*s,int i){const char*p=(const char*)col_text(s,i);return p?p:"";}
int main(int argc,char**argv){if(argc!=2&&argc!=3){fprintf(stderr,"usage: native-map UID [database-path]\n");return 1;}char*end=NULL;long uid=strtol(argv[1],&end,10);if(!end||*end||uid<=0||uid>INT_MAX){fprintf(stderr,"invalid UID\n");return 1;}const char*path=argc==3?argv[2]:"/usr/trim/var/downloadcenter/downloadcenter.db";
#ifdef __APPLE__
 const char*library="/usr/lib/libsqlite3.dylib";
#else
 const char*library="libsqlite3.so.0";
#endif
 void*lib=dlopen(library,RTLD_NOW|RTLD_LOCAL);if(!lib){fprintf(stderr,"SQLite library unavailable\n");return 1;}
 LOAD(open_db,"sqlite3_open_v2");LOAD(close_db,"sqlite3_close");LOAD(prepare,"sqlite3_prepare_v2");LOAD(step,"sqlite3_step");LOAD(finalize,"sqlite3_finalize");LOAD(bind_int,"sqlite3_bind_int");LOAD(col_int,"sqlite3_column_int64");LOAD(col_text,"sqlite3_column_text");LOAD(err_msg,"sqlite3_errmsg");LOAD(readonly,"sqlite3_db_readonly");sqlite3*db=NULL;
 if(open_db(path,&db,1,NULL)!=0){fprintf(stderr,"cannot open database read-only: %s\n",db?err_msg(db):"unknown");if(db)close_db(db);return 1;}if(readonly(db,"main")!=1){fprintf(stderr,"database is not read-only; stopped\n");close_db(db);return 1;}
 sqlite3_stmt*s=NULL;const char*sql="SELECT ID,UID,TYPE,STATE,HASH_V1,TASK_ID FROM TASKS WHERE UID=? ORDER BY ID";
 if(prepare(db,sql,-1,&s,NULL)!=0){fprintf(stderr,"schema mismatch: %s\n",err_msg(db));close_db(db);return 1;}bind_int(s,1,(int)uid);int status,count=0;printf("read_only=true; owner_uid=%ld; task names, paths, URLs and credentials omitted\n",uid);
 while((status=step(s))==100){const char*h=text(s,4),*engine=text(s,5);printf("{\"id\":%lld,\"uid\":%lld,\"type\":",col_int(s,0),col_int(s,1));quoted(text(s,2));printf(",\"state\":");quoted(text(s,3));printf(",\"hash_v1\":");quoted(hash_ok(h)?h:"[empty or non-hash]");printf(",\"task_id\":");if(hash_ok(engine))quoted(engine);else{char info[96];snprintf(info,sizeof(info),"[non-hash; length=%zu]",strlen(engine));quoted(info);}puts("}");count++;}
 int ok=status==101;if(!ok)fprintf(stderr,"query error: %s\n",err_msg(db));printf("rows=%d\n",count);finalize(s);close_db(db);dlclose(lib);return ok?0:1;
}
