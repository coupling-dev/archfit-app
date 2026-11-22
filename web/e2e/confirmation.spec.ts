import { expect, test } from '@playwright/test';
import { authenticator, ceremony, creation, docsShot, expectClean, open, session, type Handlers, type Reply } from './support';

const api='/api/repos/acme/app/passkeys';
const dummy='AQEBAQEBAQEBAQEBAQEBAQ';
const admin: Handlers={
 'GET /api/session':()=>[200,{...session,operator:true}],
 'GET /api/repos':()=>[200,{repos:[]}],
 'GET /api/admin/installations':()=>[200,{waiting:[],approved:[],unreadable:0}],
 'GET /api/admin/waiting':()=>[200,{waiting:0}],
};

test('operator lookup discards an old response and binds actions to reviewed attempt',async({page})=>{
 let release!:()=>void;
 let started!:()=>void;
 const pending=new Promise<void>((resolve)=>{started=resolve;});
 const issued: unknown[]=[];
 const handlers:Handlers={...admin,
  'POST /api/admin/confirmations/lookup':async(body)=>{
   if(body.attempt==='A'){started();await new Promise<void>((resolve)=>{release=resolve;});}
   return [200,{UserID:body.attempt==='A'?1:2,RepositoryID:10,AttemptID:body.attempt}];
  },
  'POST /api/admin/confirmations/issue':(body)=>{issued.push(body);return [200,{token:dummy}];},
 };
 await open(page,'/admin',handlers);
 await page.getByLabel('Installation ID',{exact:true}).fill('1');
 await page.getByLabel('Attempt ID',{exact:true}).fill('A');
 await page.getByRole('button',{name:'Find confirmation'}).click();
 await pending;
 await page.getByLabel('Attempt ID',{exact:true}).fill('B');
 release();
 await expect(page.getByRole('button',{name:'Find confirmation'})).toBeEnabled();
 await expect(page.getByRole('button',{name:'Issue token',exact:true})).toHaveCount(0);
 await page.getByRole('button',{name:'Find confirmation'}).click();
 await expect(page.getByText('GitHub user ID 2, repository ID 10.')).toBeVisible();
 await page.getByRole('checkbox').check();
 await page.getByRole('button',{name:'Issue token',exact:true}).click();
 expect(issued).toEqual([{installation:1,attempt:'B'}]);
 await page.getByRole('button',{name:'Hide token'}).click();
 await expect(page.getByText(dummy,{exact:true})).toHaveCount(0);
});

test('operator to owner confirmation opens passkey prompt and returns to approval with fakes',async({page})=>{
 await authenticator(page);
 let issued=false;let used=false;let registered=false;
 const handlers:Handlers={...admin,
  [`GET ${api}`]:()=>[200,{passkeys:registered?[{id:'one',created:'2026-11-22T12:00:00Z'}]:[]}],
  [`POST ${api}/confirmation`]:()=>[200,{attempt:'attempt-one',installation:1}],
  'POST /api/admin/confirmations/lookup':()=>[200,{UserID:100,RepositoryID:10,AttemptID:'attempt-one'}],
  'POST /api/admin/confirmations/issue':()=>{issued=true;return [200,{token:dummy}];},
  [`POST ${api}/register`]:(body):Reply=>{
   if(!issued||used||body.confirmation!==dummy||body.attempt!=='attempt-one')return [403,{error:'That token cannot be used.'}];
   used=true;return [200,{options:creation([])}];
  },
  [`POST ${api}/register/finish`]:(body)=>{expect(ceremony(body.credential).type).toBe('public-key');registered=true;return [200,{notice:'Passkey registered.'}];},
 };
 await open(page,'/r/acme/app/passkeys?return=%2Fr%2Facme%2Fapp%2Fowner%2Ffirst',handlers);
 await page.getByRole('button',{name:'Request confirmation'}).click();
 await expect(page.getByText('Waiting for an operator',{exact:true})).toBeVisible();
 await expectClean(page);
 await docsShot(page,'confirmation/owner-pending');
 const operator=await page.context().newPage();
 await open(operator,'/admin',handlers);
 await operator.getByLabel('Installation ID',{exact:true}).fill('1');
 await operator.getByLabel('Attempt ID',{exact:true}).fill('attempt-one');
 await operator.getByRole('button',{name:'Find confirmation'}).click();
 await operator.getByRole('checkbox').check();
 await docsShot(operator,'confirmation/operator-reviewed');
 await operator.getByRole('button',{name:'Issue token',exact:true}).click();
 await expect(operator.getByText(dummy,{exact:true})).toBeVisible();
 await operator.getByRole('button',{name:'Hide token'}).click();
 await page.getByLabel('Token from the operator').fill(dummy);
 await page.getByRole('button',{name:'Confirm and register a passkey'}).click();
 await expect(page).toHaveURL(/\/owner\/first$/);
 expect(registered).toBe(true);expect(used).toBe(true);
 await operator.close();
});

test('a failed first registration offers a fresh confirmation without reload',async({page})=>{
 let requests=0;
 await open(page,'/r/acme/app/passkeys',{
  [`GET ${api}`]:()=>[200,{passkeys:[]}],
  [`POST ${api}/confirmation`]:()=>[200,{attempt:`attempt-${++requests}`,installation:1}],
  [`POST ${api}/register`]:()=>[503,{error:'Registration failed.'}],
 });
 await page.getByRole('button',{name:'Request confirmation'}).click();
 await page.getByLabel('Token from the operator').fill(dummy);
 await page.getByRole('button',{name:'Confirm and register a passkey'}).click();
 await expect(page.getByRole('alert')).toContainText('Confirmation may have been consumed.');
 await page.getByRole('button',{name:'Request confirmation'}).click();
 expect(requests).toBe(2);
 await expect(page.getByLabel('Token from the operator')).toHaveValue('');
});
