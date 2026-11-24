import assert from 'node:assert/strict';
import { execFileSync, spawn } from 'node:child_process';
import { createHash, X509Certificate } from 'node:crypto';
import { chmodSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { join } from 'node:path';
import { connect } from 'node:tls';
import { chromium } from 'playwright-core';

const work = process.argv[2];
const exchange = join(work, 'exchange');
const worker = join(work, 'worker');
const docker = process.env.XKEEN_G18_DOCKER;
const engine = process.env.XKEEN_G18_ENGINE;
const name = 'xkeen-g18-' + process.pid;
const secret = 'synthetic G18 administrator password';
const sleep = msec => new Promise(resolve => setTimeout(resolve, msec));
let lab, validator, browser;

function command(file, args) {
  return execFileSync(file, args, {stdio:['ignore','pipe','pipe']});
}

function credentials() {
  const uiCert = join(exchange, 'server-cert.pem');
  command('openssl', ['req','-x509','-newkey','rsa:2048','-nodes','-days','1',
    '-subj','/CN=127.0.0.1','-addext','subjectAltName=IP:127.0.0.1',
    '-out',uiCert,'-keyout',join(exchange, 'server-key.pem')]);
  const ca = join(exchange, 'worker-ca.pem');
  const caKey = join(worker, 'ca-key.pem');
  command('openssl', ['req','-x509','-newkey','rsa:2048','-nodes','-days','1',
    '-subj','/CN=G18 Synthetic CA','-addext','basicConstraints=critical,CA:TRUE',
    '-out',ca,'-keyout',caKey]);

  function signed(label, cert, key, extensions) {
    const csr = join(worker, label+'.csr');
    const config = join(worker, label+'.conf');
    writeFileSync(config, extensions, {mode:0o600});
    command('openssl', ['req','-new','-newkey','rsa:2048','-nodes','-subj','/CN='+label,'-out',csr,'-keyout',key]);
    command('openssl', ['x509','-req','-days','1','-in',csr,'-CA',ca,'-CAkey',caKey,
      '-CAcreateserial','-CAserial',join(worker,'ca.srl'),'-extfile',config,'-out',cert]);
    chmodSync(key,0o600);
  }
  signed('host.docker.internal', join(worker,'cert.pem'), join(worker,'key.pem'),
    'subjectAltName=DNS:host.docker.internal\nextendedKeyUsage=serverAuth\n');
  signed('synthetic-controller', join(exchange,'worker-client.pem'), join(exchange,'worker-client-key.pem'),
    'extendedKeyUsage=clientAuth\n');
  chmodSync(join(exchange,'server-key.pem'),0o600);
  return uiCert;
}

async function port() {
  const server = createServer();
  await new Promise((ok, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', ok);
  });
  const result = server.address().port;
  await new Promise(ok => server.close(ok));
  return result;
}

function emit(milestone, value) {
  const target = join(exchange, milestone+'.json');
  writeFileSync(target+'.tmp', JSON.stringify(value), {mode:0o600,flag:'wx'});
  renameSync(target+'.tmp', target);
}

async function wait(milestone, timeout) {
  const deadline = Date.now()+timeout;
  while (Date.now()<deadline) {
    if (lab?.exitCode !== null || validator?.exitCode !== null) throw new Error('an installed fixture exited before '+milestone);
    try {
      return JSON.parse(readFileSync(join(exchange,milestone+'.json'),'utf8'));
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    await sleep(100);
  }
  throw new Error('timed out waiting for '+milestone);
}

async function waitWorker(workerPort) {
  const deadline = Date.now()+15000;
  while (Date.now()<deadline) {
    if (validator.exitCode !== null) throw new Error('validator exited before mTLS readiness');
    const ready = await new Promise(resolve => {
      const connection = connect({
        host:'127.0.0.1',port:workerPort,servername:'host.docker.internal',
        ca:readFileSync(join(exchange,'worker-ca.pem')),cert:readFileSync(join(exchange,'worker-client.pem')),
        key:readFileSync(join(exchange,'worker-client-key.pem')),minVersion:'TLSv1.3',
      });
      const done = result => { connection.destroy(); resolve(result); };
      connection.once('secureConnect',() => done(true));
      connection.once('error',() => done(false));
      connection.setTimeout(800,() => done(false));
    });
    if (ready) return;
    await sleep(100);
  }
  throw new Error('external validator did not confirm mTLS readiness');
}

function startLab(uiPort, workerPort) {
  const shell = `
test "$(cat /proc/self/cgroup)" = "0::/"
mkdir /tmp/g18-cgroup
mount -t cgroup2 -o nosuid,nodev,noexec none /tmp/g18-cgroup
test "$(cat /tmp/g18-cgroup/pids.max)" = 128
mkdir /tmp/g18-cgroup/supervisor /tmp/g18-cgroup/jobs
echo $$ >/tmp/g18-cgroup/supervisor/cgroup.procs
echo "+memory +pids +cpu" >/tmp/g18-cgroup/cgroup.subtree_control
echo "+memory +pids +cpu" >/tmp/g18-cgroup/jobs/cgroup.subtree_control
export XKEEN_TEST_CGROUP=/tmp/g18-cgroup/jobs XKEEN_TEST_XRAY=/opt/sbin/xray
uname -smr
grep -E "^(Xray|Entware)=" /usr/local/share/router-lab/versions.txt
/runner/process.test -test.run '^TestInstalledEntwareOwnerFlow$' -test.v -test.timeout 7m
test "$(find /tmp/g18-cgroup/jobs -mindepth 1 -type d | wc -l)" = 0`;
  return spawn(docker, ['run','--rm','--name',name,'--platform','linux/arm64','--cgroupns=private',
    '--memory=512m','--memory-swap=512m','--pids-limit=128','--cap-drop=ALL',
    ...['SYS_ADMIN','SYS_CHROOT','SETUID','SETGID','SETPCAP','KILL','CHOWN','DAC_OVERRIDE','SYS_PTRACE'].flatMap(cap=>['--cap-add',cap]),
    '--tmpfs','/tmp:rw,nosuid,nodev',
    '--publish','127.0.0.1:'+uiPort+':'+uiPort,
    '--mount','type=bind,src='+join(work,'release','v0.0.141')+',dst=/proof,readonly',
    '--mount','type=bind,src='+join(work,'runner')+',dst=/runner,readonly',
    '--mount','type=bind,src='+exchange+',dst=/exchange',
    '-e','XKEEN_G18_EXCHANGE=/exchange','-e','XKEEN_G18_UI_PORT='+uiPort,
    '-e','XKEEN_G18_WORKER_ORIGIN=https://host.docker.internal:'+workerPort,
    '-e','XKEEN_TEST_SERVER_BINARY=/proof/xkeen-ui-server-linux-arm64',
    '--entrypoint','/bin/sh',process.env.LAB_IMAGE,'-ec',shell], {stdio:['ignore','inherit','inherit']});
}

async function runConsole(page, fixture) {
  const {origin,templateId,proxyPort,denyPort,witnessPort,agentOrigin} = fixture;
  const get = label => page.getByRole('button',{name:label,exact:true});
  const editor = () => origin+'/ui/templates/item?'+new URLSearchParams({templateId});
  const uiRead = route => page.evaluate(async path => {
    const response = await fetch(path);
    if (!response.ok) throw new Error('read-only evidence unavailable');
    return response.json();
  }, route);
  const template = () => uiRead('/ui/v1/templates/item?'+new URLSearchParams({templateId}));
  await page.goto(origin+'/ui/');
  const password = page.getByLabel('Пароль администратора');
  await password.fill(secret);
  await password.press('Enter');
  await page.getByRole('heading',{name:'Обзор',exact:true}).waitFor();
  await page.getByRole('link',{name:'Шаблоны',exact:true}).click();
  await get('Создать шаблон').click();
  await page.getByLabel('Идентификатор шаблона').fill(templateId);
  await get('Создать').click();
  await page.getByRole('link',{name:templateId,exact:true}).click();
  const section = page.getByLabel('Секция Xray');
  const json = page.getByLabel('JSON',{exact:true});
  const add = get('Добавить элемент');

  await section.selectOption('log');
  await json.fill('{"loglevel":"warning"}');
  await get('Создать элемент').click();
  await page.getByRole('heading',{name:'Сырой JSON элемента',exact:true}).waitFor();
  async function append(group,value) {
    const version = (await template()).version;
    await add.click();
    await section.selectOption(group);
    await json.fill(JSON.stringify(value));
    const reply = page.waitForResponse(r => r.url().includes('/ui/v1/templates/item?') && r.request().method()==='POST');
    await add.click();
    const saved = await reply;
    assert.equal(saved.status(),200);
    const result = await saved.json();
    await page.waitForFunction(async ({templateId,version})=>{
      const response=await fetch('/ui/v1/templates/item?'+new URLSearchParams({templateId}));
      const data=await response.json();
      return data.version!==version;
    },{templateId,version});
    await page.getByText('Черновик сохранён.',{exact:true}).waitFor();
    return result.selected.id;
  }
  await append('inbounds',{port:Number(proxyPort),listen:'127.0.0.1',protocol:'socks',tag:'g18-socks'});
  await append('outbounds',{tag:'direct',protocol:'freedom'});
  await append('outbounds',{tag:'block',protocol:'blackhole'});
  await append('rules',{ruleTag:'g18-fixed-deny',type:'field',port:denyPort,outboundTag:'block'});
  const witness = {'ruleTag':'g18-witness',type:'field',port:'$'+'{witness.port}',outboundTag:'direct'};
  const witnessID = await append('rules',witness);
  await get('Переменные').click();
  await page.getByLabel('Значение по умолчанию witness.port',{exact:true}).fill('');
  await get('Сохранить определения').click();
  await page.getByText('Определения сохранены.',{exact:true}).waitFor();

  await page.getByRole('link',{name:'Обзор',exact:true}).click();
  await page.getByRole('link',{name:'Подключить роутер',exact:true}).click();
  await page.getByLabel('Имя роутера').fill('Лабораторный роутер');
  await page.getByLabel('Шаблон',{exact:true}).selectOption(templateId);
  const portInput = page.getByLabel('witness.port',{exact:true});
  assert.equal(await portInput.getAttribute('required'),'');
  await portInput.fill(witnessPort);
  await get('Проверить и подготовить').click();
  await page.getByText('Желаемая ревизия 1',{exact:true}).waitFor();
  await page.getByRole('link',{name:'Подключить агента',exact:true}).click();
  await get('Выдать приглашение').click();
  const invitation = page.getByRole('textbox',{name:'Одноразовое приглашение'});
  await invitation.waitFor();
  const routerId = new URL(page.url()).searchParams.get('routerId');
  assert.ok(routerId);
  await page.getByText(agentOrigin,{exact:true}).waitFor();
  writeFileSync(join(exchange,'invitation'), await invitation.inputValue(), {mode:0o600,flag:'wx'});
  emit('router',{routerId});
  console.log('Chrome created exact template elements, set the required value and prepared/enrolled via normal controls');

  await wait('first',90000);
  await wait('offline',205000);
  async function change(group,id,source) {
    await page.goto(editor());
    await section.selectOption(group);
    if (id) {
      await page.getByLabel(/^Элемент/).selectOption(id);
      await page.locator('.editor-id').getByText(id,{exact:true}).waitFor();
    }
    await json.fill(JSON.stringify(source));
    const reply = page.waitForResponse(r => r.url().includes('/ui/v1/templates/item?') && r.request().method()==='POST');
    await get('Сохранить черновик').click();
    assert.equal((await reply).status(),200);
    await page.getByText('Черновик сохранён.',{exact:true}).waitFor();
  }
  async function publish(desired) {
    await get('Просмотреть последствия публикации').click();
    await page.getByRole('heading',{name:'Последствия публикации',exact:true}).waitFor();
    await page.getByText('Нет связи',{exact:true}).waitFor();
    await get('Перейти к подтверждению').click();
    await get('Подтвердить публикацию').click();
    await page.getByText(/Снимок выпуска принят · версия/).waitFor();
    const proof=await uiRead('/ui/v1/templates/publication?'+new URLSearchParams({templateId}));
    assert.equal(proof.members.find(member=>member.routerId===routerId).desiredRevision,String(desired));
  }
  await change('log',null,{loglevel:'error'});
  await publish(2);
  await change('rules',witnessID,{...witness,outboundTag:'block'});
  await publish(3);
  emit('published',true);
  console.log('Chrome confirmed two distinct F18 publications while the installed agent was offline');
  const proof = await wait('complete',105000);
  assert.equal(proof.routerId,routerId);
  await page.getByRole('link',{name:'Обзор',exact:true}).click();
  await page.getByText('Связь: на связи',{exact:true}).waitFor();
  await page.getByText('Желаемая ревизия 3',{exact:true}).waitFor();
  await page.getByText('Последняя подтверждённая ревизия 3',{exact:true}).waitFor();
  const list = await uiRead('/ui/v1/routers');
  const row = list.routers.find(item=>item.routerId===routerId);
  assert.equal(row.contact,'online');
  assert.equal(row.desiredRevision,'3');
  assert.equal(row.appliedRevision,'3');
  emit('browser-complete',true);
}

try {
  assert.ok(engine.startsWith('unix://'), 'a local Unix Docker Engine is required');
  const uiCert = credentials();
  const workerPort = await port();
  const uiPort = await port();
  const clientHash = new X509Certificate(readFileSync(join(exchange,'worker-client.pem'))).fingerprint256.replaceAll(':','').toLowerCase();
  validator = spawn(join(worker,'validator'), ['-docker',docker,'-engine',engine.slice(7),
    '-image',readFileSync(join(worker,'image'),'utf8').trim(),'-state',worker,
    '-listen','127.0.0.1:'+workerPort,'-cert',join(worker,'cert.pem'),'-key',join(worker,'key.pem'),
    '-client-ca',join(exchange,'worker-ca.pem'),'-client-sha256',clientHash], {stdio:['ignore','inherit','inherit']});
  await waitWorker(workerPort);
  lab = startLab(uiPort, workerPort);
  const finished = new Promise((resolve,reject) => {
    lab.once('error',reject);
    lab.once('exit',resolve);
  });
  const fixture = await wait('fixture',35000);
  const publicKey = new X509Certificate(readFileSync(uiCert)).publicKey.export({type:'spki',format:'der'});
  const pin = createHash('sha256').update(publicKey).digest('base64');
  browser = await chromium.launch({executablePath:process.env.CHROME_EXECUTABLE,args:['--ignore-certificate-errors-spki-list='+pin]});
  const page = await browser.newPage({viewport:{width:1080,height:720}});
  page.setDefaultTimeout(12000);
  await runConsole(page,fixture);
  assert.equal(await finished,0);
} finally {
  await browser?.close();
  validator?.kill();
  if (lab && lab.exitCode===null) {
    try { command(docker,['rm','-f',name]); } catch {}
    lab.kill();
  }
}
