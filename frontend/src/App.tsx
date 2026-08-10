import { type ReactElement, useCallback, useEffect, useMemo, useState } from 'react'
import { api, ButtonPayload, Choices, DeviceState, DPIStage, Haptics, onDeviceUpdate, Profile } from './backend'
import productImage from './productImage'
import productSideImage from './productSideImage'

type Page = 'dashboard' | 'dpi' | 'profiles' | 'buttons' | 'haptics'
type Toast = { id: number; text: string; error?: boolean }
type OperationState = { text: string; tone?: 'saving' | 'saved' | 'error' }
const rates = [8000, 4000, 2000, 1000, 500, 250, 125]
const dpiColors = ['#ff4a43','#30dc72','#2788ff','#ff7a24','#b54cff']

function friendlyName(value: string | undefined, fallback: string) {
  if (!value) return fallback
  const factory = value.match(/^PROFILE_NAME_(.+)$/i)
  const clean = (factory?.[1] || value).replaceAll('_', ' ').trim()
  return clean.toLowerCase().replace(/\b\w/g, letter => letter.toUpperCase())
}

function friendlyDeviceName(value: string) {
  if (/superstri+ke/i.test(value)) return 'PRO X2 Superstrike'
  return friendlyName(value, 'Superstrike')
}

function editableProfileName(profile: Profile | undefined) {
  if (!profile) return ''
  if (!profile.name) return profile.index === 1 ? 'Default' : `Profile ${profile.index}`
  if (/^PROFILE_NAME_/i.test(profile.name)) return friendlyName(profile.name, `Profile ${profile.index}`)
  return profile.name
}

function InlineStatus({state,idle='Stored onboard'}: {state?:OperationState;idle?:string}) {
  return <div className={`inline-status ${state?.tone||'idle'}`}><i/>{state?.text||idle}</div>
}

function Icon({name}: {name: Page | 'refresh' | 'check' | 'edit' | 'battery' | 'signal'}) {
  const paths: Record<string, ReactElement> = {
    dashboard: <><rect x="4" y="4" width="6" height="6" rx="1"/><rect x="14" y="4" width="6" height="6" rx="1"/><rect x="4" y="14" width="6" height="6" rx="1"/><rect x="14" y="14" width="6" height="6" rx="1"/></>,
    profiles: <><path d="M5 5h14v4H5zM5 11h14v4H5zM5 17h14v2H5z"/></>,
    buttons: <><path d="M12 3c-4.2 0-7 3.2-7 8.4V16c0 3.1 2.4 5 7 5s7-1.9 7-5v-4.6C19 6.2 16.2 3 12 3Z"/><path d="M12 3v7M9 7h6" className="cut"/></>,
    haptics: <><path d="M8 8a5.7 5.7 0 0 0 0 8M5 5a9.9 9.9 0 0 0 0 14M16 8a5.7 5.7 0 0 1 0 8M19 5a9.9 9.9 0 0 1 0 14"/><circle cx="12" cy="12" r="2"/></>,
    refresh: <path d="M20 6v5h-5M4 18v-5h5M6.1 9a7 7 0 0 1 11.7-2.6L20 11M4 13l2.2 4.5A7 7 0 0 0 18 15"/>,
    check: <path d="m5 12 4 4L19 6"/>, edit: <path d="m14 5 5 5M4 20l3.6-.7L19 8a2.1 2.1 0 0 0-3-3L4.7 16.3 4 20Z"/>,
    battery: <><rect x="3" y="7" width="17" height="10" rx="2"/><path d="M22 10v4"/></>,
    signal: <><path d="M5 19v-3M10 19v-7M15 19V8M20 19V4"/></>,
  }
  return <svg className="icon" viewBox="0 0 24 24" aria-hidden="true">{paths[name]}</svg>
}

function MouseArt({interactive = false, selected, onSelect, visibleMarkers=[0,1,2]}: {interactive?: boolean; selected?: number; onSelect?: (i: number) => void; visibleMarkers?:number[]}) {
  const marker = (index: number, x: number, y: number) => <g onClick={() => onSelect?.(index)} className={`mouse-marker ${selected === index ? 'selected' : ''}`}><circle cx={x} cy={y} r="30"/><text x={x} y={y + 2}>{index + 1}</text></g>
  return <div className={`mouse-art ${interactive ? 'interactive' : ''}`} role="img" aria-label="PRO X2 Superstrike mouse">
    <img src={productImage} alt="" draggable="false"/>
    {interactive && <svg className="mouse-hitareas" viewBox="0 0 806 1601" aria-hidden="true">
      {visibleMarkers.includes(0)&&marker(0, 285, 300)}
      {visibleMarkers.includes(1)&&marker(1, 520, 300)}
      {visibleMarkers.includes(2)&&marker(2, 403, 335)}
    </svg>}
  </div>
}

function MouseSideArt({interactive=false,selected,onSelect}: {interactive?:boolean;selected?:number;onSelect?:(index:number)=>void}) {
  const marker=(index:number,x:number,y=150)=><g onClick={()=>onSelect?.(index)} className={`side-marker ${selected===index?'selected':''}`}><circle cx={x} cy={y} r="17"/><text x={x} y={y+1}>{index+1}</text></g>
  return <><img className="mouse-side-art" src={productSideImage} alt="PRO X2 Superstrike side view" draggable="false"/>{interactive&&<svg className="side-hitareas" viewBox="0 0 974 311" aria-hidden="true">{marker(4,380)}{marker(3,522,138)}</svg>}</>
}

function EmptyState({state}: {state: DeviceState}) {
  const denied=state.permissionDenied
  return <div className={`device-wait-screen ${denied?'permission':''}`}>
    <section className="wait-copy-panel">
      <span className="settings-kicker">DEVICE CONNECTION</span>
      <h1>{denied?'Access required':'Connect your Superstrike'}</h1>
      <p>{denied?'The mouse is visible to Linux, but Superstrike Control cannot open it yet. Install the included device-access rule, then reconnect the mouse.':'Power on the mouse and connect it by cable or LIGHTSPEED receiver. The app will open your settings automatically when the device responds.'}</p>
      <div className={`wait-status ${denied?'blocked':''}`}><i/><div><strong>{denied?'Device found · access blocked':'Searching for PRO X2 Superstrike'}</strong><small>{denied?'Reconnect after granting access':'Automatic detection is running'}</small></div></div>
      <div className="wait-checklist">
        <div><b>01</b><span><strong>Power on the mouse</strong><small>Use wired or LIGHTSPEED mode</small></span></div>
        <div><b>02</b><span><strong>Connect to this computer</strong><small>Direct USB connections work too</small></span></div>
        <div><b>03</b><span><strong>Keep this window open</strong><small>No refresh or restart is required</small></span></div>
      </div>
    </section>
    <section className="wait-product-panel"><span>DEVICE PREVIEW</span><MouseArt/><div><strong>PRO X2 Superstrike</strong><small>HID++ 4.2 gaming mouse</small></div></section>
    <footer className="wait-footer"><span className={`strip-dot ${denied?'warning':''}`}/><strong>{denied?'Permission setup required':'Waiting for device'}</strong><small>{denied?'Run the included installer if the access rule is missing.':'This page updates as soon as the mouse is available.'}</small></footer>
  </div>
}

function Metric({label, value, suffix, detail, icon}: {label: string; value: string | number; suffix?: string; detail: string; icon: 'profiles'|'signal'|'battery'}) {
  return <article className="metric glass"><div className="metric-top"><span>{label}</span><Icon name={icon}/></div><div className="metric-value">{value}<small>{suffix}</small></div><p>{detail}</p></article>
}

function Dashboard({state, notify}: {state: DeviceState; notify: (s: string, e?: boolean) => void}) {
  const [selectedRate, setSelectedRate] = useState(0)
  const [savingRate, setSavingRate] = useState(false)
  const [operation,setOperation] = useState<OperationState>()
  useEffect(() => {
    if (!state.connected) return
    api.profiles().then(profiles => {
      const active = profiles.find(profile => profile.active)
      if (active) setSelectedRate(active.pollingRate)
    }).catch(() => {})
  }, [state.connected, state.profile])
  const chooseRate = async (rate: number) => {
    if (savingRate || rate === selectedRate) return
    const previous = selectedRate
    setSelectedRate(rate); setSavingRate(true); setOperation({text:'Writing polling rate…',tone:'saving'})
    try {
      const profiles = await api.profiles()
      const active = profiles.find(profile => profile.active)
      if (!active) throw new Error('No active onboard profile was found')
      await api.updateRate(active.sector, rate)
      const updated = await api.profiles()
      setSelectedRate(updated.find(profile => profile.active)?.pollingRate || rate)
      setOperation({text:`${rate} Hz stored in the active profile`,tone:'saved'})
      notify(`Polling rate set to ${rate} Hz`)
    } catch (error) {
      setSelectedRate(previous); setOperation({text:'Polling-rate write failed',tone:'error'}); notify(String(error), true)
    } finally { setSavingRate(false) }
  }
  if (!state.connected) return <EmptyState state={state}/>
  return <div className="hub-dashboard page-enter">
    <section className="hub-panel product-view"><MouseArt/></section>
    <section className="hub-panel side-view"><MouseSideArt/></section>
    <section className="hub-panel rate-overview"><div><h2>Stored polling rate</h2><p>Select the USB report frequency for the active onboard profile.</p><InlineStatus state={operation} idle="Stored in the active profile"/></div><div className={`rate-track ${savingRate ? 'saving' : ''}`}>{rates.slice().reverse().map(rate=><button key={rate} className={selectedRate===rate?'active':''} onClick={()=>chooseRate(rate)} disabled={savingRate}>{rate >= 1000 ? `${rate/1000}K` : rate}<small>Hz</small></button>)}</div></section>
  </div>
}

function DPIPage({notify}: {notify: (s: string, e?: boolean) => void}) {
  const [activeProfile,setActiveProfile] = useState<Profile>()
  const [drafts,setDrafts] = useState<DPIStage[]>([]), [selected,setSelected] = useState(0)
  const [busy,setBusy] = useState(true), [saving,setSaving] = useState<number>()
  const [operation,setOperation] = useState<OperationState>()
  const load = useCallback(async()=>{setBusy(true);try{const list=await api.profiles();const active=list.find(profile=>profile.active)||list[0];setActiveProfile(active);setDrafts(active?.dpiStages||[]);if(active)setSelected(active.currentDpiStage>=0?active.currentDpiStage:active.defaultDpiStage)}catch(error){notify(String(error),true)}finally{setBusy(false)}},[notify])
  useEffect(()=>{load()},[load])
  const edit=(index:number,value:number)=>setDrafts(current=>current.map(stage=>stage.index===index?{...stage,x:value,y:value}:stage))
  const save=async(stage:DPIStage,enabled=stage.enabled,makeDefault=false)=>{if(!activeProfile||saving!==undefined)return;setSaving(stage.index);setOperation({text:makeDefault?'Changing profile default…':`Writing slot ${stage.index+1}…`,tone:'saving'});try{await api.updateDPIStage(activeProfile.sector,stage.index,Math.max(100,Math.min(44000,stage.x)),Math.max(100,Math.min(44000,stage.y)),enabled,makeDefault);setOperation({text:makeDefault?`Slot ${stage.index+1} is the stored default`:`Slot ${stage.index+1} stored onboard`,tone:'saved'});notify(makeDefault?`DPI slot ${stage.index+1} set as default`:`DPI slot ${stage.index+1} saved to the mouse`);await load()}catch(error){setOperation({text:'DPI write failed',tone:'error'});notify(String(error),true)}finally{setSaving(undefined)}}
  const selectStage=async(stage:DPIStage)=>{if(!activeProfile||saving!==undefined||!stage.enabled)return;setSaving(stage.index);setSelected(stage.index);setOperation({text:`Applying ${stage.x} DPI live…`,tone:'saving'});try{await api.selectDPI(activeProfile.sector,stage.x);setOperation({text:`Slot ${stage.index+1} is active now`,tone:'saved'});notify(`DPI slot ${stage.index+1} selected`);await load()}catch(error){setOperation({text:'Could not select the DPI slot',tone:'error'});notify(String(error),true)}finally{setSaving(undefined)}}
  if(busy&&!activeProfile)return <Loader text="Reading active profile"/>
  if(!activeProfile)return <EmptyState state={{connected:false,permissionDenied:false} as DeviceState}/>
  if(!activeProfile.hasDpiStages)return <div className="dpi-unsupported"><h1>DPI stages unavailable</h1><p>This onboard profile uses an older layout that does not safely expose five independent stages.</p></div>
  const selectedStage=drafts.find(stage=>stage.index===selected)||drafts[0]
  return <div className="dpi-hub-page page-enter">
    <main className="dpi-work-panel">
      <header className="dpi-title"><div><h1>DPI</h1><p>Five sensitivity slots stored directly on the mouse.</p></div><div className="dpi-title-actions"><div className="dpi-state-legend"><span><i className="live"/>Current</span><span><i className="stored"/>Profile default</span></div><strong>{drafts.filter(stage=>stage.enabled).length} / 5 enabled</strong></div></header>
      <section className="dpi-context-bar"><div><span>Active onboard profile</span><strong>{friendlyName(activeProfile.name,`Profile ${activeProfile.index}`)}</strong></div><div><span>Live DPI slot</span><strong>Slot {(activeProfile.currentDpiStage>=0?activeProfile.currentDpiStage:activeProfile.defaultDpiStage)+1}</strong></div><div><span>Selected value</span><strong>{selectedStage?.x||'—'} DPI</strong></div></section>
      <div className="dpi-stage-head"><span>Slot</span><span>DPI</span><span>Enabled</span><span>Default</span></div>
      <div className="dpi-stage-list">{drafts.map(stage=><div key={stage.index} className={`dpi-stage-row onboard ${selected===stage.index?'selected':''} ${!stage.enabled?'disabled':''}`} onClick={()=>setSelected(stage.index)}>
        <button className={`dpi-active-radio ${activeProfile.currentDpiStage===stage.index?'active':''}`} title="Select this DPI now" disabled={!stage.enabled||saving!==undefined} onClick={event=>{event.stopPropagation();selectStage(stage)}} />
        <span className="dpi-stage-index" style={{borderColor:dpiColors[stage.index]}}>{stage.index+1}</span>
        <input className="dpi-stage-range" type="range" min="100" max="44000" step="50" disabled={!stage.enabled||saving!==undefined} value={stage.x} onChange={event=>edit(stage.index,+event.target.value)} onPointerUp={event=>{const value=+event.currentTarget.value;save({...stage,x:value,y:value})}} />
        <input className="dpi-stage-number" type="number" min="100" max="44000" step="50" disabled={!stage.enabled||saving!==undefined} value={stage.x} onChange={event=>edit(stage.index,+event.target.value)} onBlur={()=>save(stage)} onKeyDown={event=>{if(event.key==='Enter')save(stage)}}/>
        <span className="dpi-unit">DPI</span>
        <button className={`switch dpi-stage-switch ${stage.enabled?'on':''}`} disabled={saving!==undefined} onClick={event=>{event.stopPropagation();save(stage,!stage.enabled,false)}}><i/></button>
        <button className={`dpi-default-button ${activeProfile.defaultDpiStage===stage.index?'active':''}`} disabled={!stage.enabled||saving!==undefined||activeProfile.defaultDpiStage===stage.index} onClick={event=>{event.stopPropagation();save(stage,true,true)}}>{activeProfile.defaultDpiStage===stage.index?'Default':'Set default'}</button>
        <button className="dpi-row-save" disabled={!stage.enabled||saving!==undefined} onClick={event=>{event.stopPropagation();save(stage)}}>{saving===stage.index?'Saving…':'Save'}</button>
      </div>)}</div>
      <div className="dpi-save-note"><p>The filled circle is the live DPI. “Default” is the slot stored for the next profile load. Mouse DPI controls cycle through enabled slots.</p><InlineStatus state={operation} idle="Changes are stored directly on the mouse"/></div>
    </main>
  </div>
}

function ProfilesPage({notify}: {notify: (s: string, e?: boolean) => void}) {
  const [profiles,setProfiles] = useState<Profile[]>([]), [busy,setBusy] = useState(true), [selected,setSelected] = useState(0), [name,setName] = useState('')
  const [operation,setOperation] = useState<OperationState>()
  const load = useCallback(async()=>{setBusy(true);try{const list=await api.profiles();setProfiles(list);setSelected(current=>list[current]?current:Math.max(0,list.findIndex(profile=>profile.active)))}catch(error){notify(String(error),true)}finally{setBusy(false)}},[notify])
  useEffect(()=>{load()},[load])
  const profile=profiles[selected]
  const shownName=editableProfileName(profile)
  useEffect(()=>{setName(editableProfileName(profile))},[profile?.sector,profile?.name])
  const action = async(label:string,fn:()=>Promise<unknown>)=>{setOperation({text:'Updating onboard profile…',tone:'saving'});try{await fn();setOperation({text:label,tone:'saved'});notify(label);await load()}catch(error){setOperation({text:'Could not update the profile',tone:'error'});notify(String(error),true)}}
  if(busy&&!profiles.length)return <Loader text="Reading onboard profiles"/>
  if(!profile)return <EmptyState state={{connected:false,permissionDenied:false} as DeviceState}/>
  const defaultStage=profile.dpiStages.find(stage=>stage.index===profile.defaultDpiStage)
  return <div className="profiles-manager page-enter">
    <aside className="profile-browser">
      <header><div><span>ONBOARD MEMORY</span><h1>Profiles</h1></div><button className="icon-button" onClick={load} title="Refresh"><Icon name="refresh"/></button></header>
      <div className="profile-browser-list">{profiles.map((item,index)=><button key={item.sector} className={`${selected===index?'selected':''} ${item.active?'active':''} ${!item.enabled?'disabled':''}`} onClick={()=>{setSelected(index);setOperation(undefined)}}><span className="browser-index">{String(index+1).padStart(2,'0')}</span><span><strong>{friendlyName(item.name,`Profile ${item.index}`)}</strong><small>{item.enabled?'Stored onboard':'Disabled'} · {item.active?'In use now':`${item.pollingRate} Hz`}</small></span><i/></button>)}</div>
      <footer><span>{profiles.length} onboard profiles</span><strong>{friendlyName(profiles.find(item=>item.active)?.name,'None')} active</strong></footer>
    </aside>
    <main className="profile-settings-panel profile-settings-wide">
      <header className="profile-summary-header"><div><span>PROFILE {String(profile.index).padStart(2,'0')}</span><h1>{friendlyName(profile.name,`Profile ${profile.index}`)}</h1><p>{profile.active?'Currently active on the mouse':'Stored in onboard memory'}</p></div><div className={`profile-live-badge ${profile.active?'active':''}`}><i/>{profile.active?'In use now':'Stored profile'}</div><section className="profile-summary-stats"><div><span>Default DPI</span><strong>{defaultStage?.x||profile.dpiX}</strong></div><div><span>Stored polling</span><strong>{profile.pollingRate} Hz</strong></div><div><span>DPI slots</span><strong>{profile.dpiStages.filter(stage=>stage.enabled).length} enabled</strong></div><div><span>Onboard state</span><strong>{profile.enabled?'Enabled':'Disabled'}</strong></div></section></header>
      <div className="profile-detail-body">
        <div className="profile-overview-grid"><section className="profile-detail-card"><span className="settings-kicker">IDENTITY</span><h2>Profile name</h2><p className="section-description">The name shown when managing this onboard slot.</p><label>Name</label><input value={name} maxLength={24} onChange={event=>setName(event.target.value)}/><button className="button subtle full" disabled={!name.trim()||name.trim()===shownName} onClick={()=>action('Profile renamed',()=>api.rename(profile.sector,name.trim()))}>Save name</button></section><section className="profile-detail-card"><span className="settings-kicker">ONBOARD BEHAVIOR</span><h2>Profile state</h2><p className="section-description">Control whether this profile is available and active.</p><div className="profile-setting-line"><div><strong>Enabled</strong><small>Available when cycling profiles on the mouse</small></div><button className={`switch ${profile.enabled?'on':''}`} onClick={()=>action(profile.enabled?'Profile disabled':'Profile enabled',()=>api.enable(profile.index,!profile.enabled))}><i/></button></div><div className="profile-setting-line"><div><strong>Live profile</strong><small>{profile.active?'This profile is in use now':'Another profile is currently in use'}</small></div><span className={`profile-state-dot ${profile.active?'on':''}`}/></div>{!profile.active&&<button className="button primary full" disabled={!profile.enabled} onClick={()=>action('Profile activated',()=>api.activate(profile.sector))}>Use this profile</button>}</section></div>
        <div className="profile-data-grid"><section className="profile-detail-card profile-dpi-summary"><span className="settings-kicker">STORED DPI</span><h2>DPI slots</h2><p className="section-description">The five sensitivity stages saved in this profile.</p>{profile.hasDpiStages?<div className="profile-dpi-list">{profile.dpiStages.map(stage=><div key={stage.index} className={!stage.enabled?'disabled':''}><i style={{background:dpiColors[stage.index]}}/><span>Slot {stage.index+1}</span><strong>{stage.x===stage.y?stage.x:`${stage.x} × ${stage.y}`} DPI</strong>{profile.defaultDpiStage===stage.index&&<em>Default</em>}<small>{stage.enabled?'Enabled':'Disabled'}</small></div>)}</div>:<p className="profile-dpi-unavailable">Independent DPI slots aren’t exposed by this profile layout.</p>}</section><section className="profile-detail-card profile-button-summary"><span className="settings-kicker">STORED CONTROLS</span><h2>Button mappings</h2><p className="section-description">Assignments saved in this onboard profile.</p><div className="profile-button-list">{profile.buttonMappings.map(button=><div key={button.index}><i>{button.index+1}</i><span>{button.name}</span><strong>{button.assignment}</strong></div>)}</div></section></div>
      </div>
      <footer className="profile-operation"><InlineStatus state={operation} idle="Changes are stored directly on the mouse"/></footer>
    </main>
  </div>
}

function Buttons({notify}: {notify: (s: string, e?: boolean) => void}) {
  const [data, setData] = useState<ButtonPayload>(), [choices, setChoices] = useState<Choices>(), [selected, setSelected] = useState(0)
  const [kind,setKind] = useState(0), [code,setCode] = useState(0), [mods,setMods] = useState(0), [saving,setSaving] = useState(false)
  const [operation,setOperation] = useState<OperationState>()
  const load = useCallback(async () => { try { const [b,c] = await Promise.all([api.buttons(), api.choices()]); setData(b); setChoices(c) } catch(e) { notify(String(e), true) } }, [notify])
  useEffect(() => { load() }, [load]); const item = data?.buttons[selected]
  useEffect(()=>{if(item){setKind(Math.min(item.action.Kind,4));setCode(item.action.Code||1);setMods(item.action.Mods||0)}},[item?.index,item?.action.Kind,item?.action.Code,item?.action.Mods])
  if(!data||!choices||!item)return <Loader text="Reading button assignments"/>
  const categories=[{name:'Mouse',kind:1},{name:'Keyboard',kind:2},{name:'Media',kind:3},{name:'Functions',kind:4},{name:'Disabled',kind:0}]
  const options=kind===1?choices.mouse:kind===2?choices.keys:kind===3?choices.media:kind===4?choices.functions:[]
  const save=async()=>{setSaving(true);setOperation({text:'Writing assignment…',tone:'saving'});try{await api.setButton(data.sector,item.index,kind,code,mods);setOperation({text:`${item.name} stored in this profile`,tone:'saved'});notify(`${item.name} remapped`);await load()}catch(error){setOperation({text:'Button assignment failed',tone:'error'});notify(String(error),true)}finally{setSaving(false)}}
  return <div className="button-mapper page-enter">
    <section className="button-visual-panel">
      <div className="button-top-view"><MouseArt interactive selected={selected} onSelect={index=>{if(data.buttons[index])setSelected(index)}}/></div>
      <div className="button-side-view"><MouseSideArt interactive selected={selected} onSelect={index=>setSelected(index)}/><div className="side-selection"><span>{selected+1}</span><strong>{item.name}</strong></div></div>
      <nav className="physical-button-list">{data.buttons.map((button,index)=><button key={button.index} className={selected===index?'selected':''} title={button.name} onClick={()=>setSelected(index)}><i>{index+1}</i><span>{button.name}</span></button>)}</nav>
    </section>
    <aside className="mapping-editor-panel">
      <header><div><span>SELECTED CONTROL</span><h1>{item.name}</h1><p>{item.description}</p></div><i className="mapping-index">{selected+1}</i></header>
      <div className="mapping-category-tabs">{categories.map(category=><button key={category.kind} className={kind===category.kind?'selected':''} onClick={()=>{setKind(category.kind);const list=category.kind===1?choices.mouse:category.kind===2?choices.keys:category.kind===3?choices.media:category.kind===4?choices.functions:[];setCode(list[0]?.code||0)}}>{category.name}</button>)}</div>
      <section className="mapping-options"><span>{kind===0?'BUTTON STATE':`${categories.find(category=>category.kind===kind)?.name.toUpperCase()} ASSIGNMENTS`}</span>{kind===0?<button className="mapping-option selected"><strong>Disabled</strong><small>The selected control will do nothing</small></button>:options.map(option=><button key={option.code} className={`mapping-option ${code===option.code?'selected':''}`} onClick={()=>setCode(option.code)}><strong>{option.name}</strong>{code===option.code&&<Icon name="check"/>}</button>)}</section>
      <section className={`mapping-modifiers ${kind===2?'visible':''}`}><span>MODIFIERS</span><div>{[['Ctrl',1],['Shift',2],['Alt',4],['Super',8]].map(([name,bit])=><button key={String(name)} className={(mods&Number(bit))?'selected':''} disabled={kind!==2} onClick={()=>setMods(mods^Number(bit))}>{name}</button>)}</div></section>
      <footer><div className="mapping-footer-copy"><span>New assignment</span><strong>{kind===0?'Disabled':options.find(option=>option.code===code)?.name||'Select an assignment'}</strong><InlineStatus state={operation} idle="Stored in the active onboard profile"/></div><button className="button primary" disabled={saving||(kind!==0&&!options.some(option=>option.code===code))} onClick={save}>{saving?'Saving…':'Apply assignment'}</button></footer>
    </aside>
  </div>
}

function AssignmentModal({item,choices,onClose,onSave}: {item: ButtonPayload['buttons'][number]; choices: Choices; onClose:()=>void; onSave:(kind:number,code:number,mods:number)=>void}) {
  const initial = Math.min(item.action.Kind,4), [kind,setKind] = useState(initial), [code,setCode] = useState(item.action.Code || 1), [mods,setMods] = useState(item.action.Mods || 0)
  const categories = [{n:'Disabled',k:0},{n:'Mouse button',k:1},{n:'Keyboard key',k:2},{n:'Media key',k:3},{n:'Mouse function',k:4}]
  const list = kind === 1 ? choices.mouse : kind === 2 ? choices.keys : kind === 3 ? choices.media : kind === 4 ? choices.functions : []
  useEffect(() => { if (list.length && !list.some(v => v.code === code)) setCode(list[0].code) }, [kind])
  return <div className="modal-backdrop" onMouseDown={e => e.currentTarget === e.target && onClose()}><div className="modal"><div className="modal-head"><div><span>REASSIGN CONTROL</span><h2>{item.name}</h2></div><button onClick={onClose}>×</button></div><label>Action type</label><div className="category-grid">{categories.map(c => <button className={kind === c.k ? 'selected' : ''} onClick={() => setKind(c.k)} key={c.k}>{c.n}</button>)}</div>{kind !== 0 && <><label>Assignment</label><select value={code} onChange={e => setCode(+e.target.value)}>{list.map(c => <option value={c.code} key={c.code}>{c.name}</option>)}</select></>}{kind === 2 && <><label>Modifiers</label><div className="modifier-row">{[['Ctrl',1],['Shift',2],['Alt',4],['Super',8]].map(([name,bit]) => <button key={String(name)} className={(mods & Number(bit)) ? 'selected' : ''} onClick={() => setMods(mods ^ Number(bit))}>{name}</button>)}</div></>}<div className="modal-actions"><button className="button subtle" onClick={onClose}>Cancel</button><button className="button primary" onClick={() => onSave(kind,code,mods)}>Save assignment</button></div></div></div>
}

function HapticsPage({notify}: {notify: (s: string, e?: boolean) => void}) {
  const [data,setData] = useState<Haptics>(), [selected,setSelected] = useState(0)
  const [operation,setOperation] = useState<OperationState>()
  const load = useCallback(async () => { try { setData(await api.haptics()) } catch(e) { notify(String(e), true) } }, [notify]); useEffect(() => { load() }, [load])
  const change = (field: 'haptics'|'actuation'|'rapidTrigger', value: number) => setData(d => d ? {...d,buttons:d.buttons.map((b,i)=>i===selected?{...b,[field]:value}:b)} : d)
  const save = async (field: string, value: number) => { setOperation({text:'Applying switch setting…',tone:'saving'});try { await api.setHaptic(selected,field,value);setOperation({text:'Applied live and stored on the mouse',tone:'saved'});notify('Haptic setting saved') } catch(e) { setOperation({text:'Switch setting failed',tone:'error'});notify(String(e),true);load() } }
  const button = data?.buttons[selected]
  if (!data || !button) return <Loader text="Reading analog switch settings"/>
  return <div className="haptics-mapper page-enter">
    <section className="haptics-device-panel">
      <header><span>ANALOG SWITCH ENGINE</span><h1>Haptic response</h1><p>Select a primary button to tune its physical response.</p></header>
      <div className="haptics-mouse"><MouseArt interactive selected={selected} visibleMarkers={[0,1]} onSelect={index=>setSelected(index)}/></div>
      <nav className="haptic-button-picker">{data.buttons.map((item,index)=><button key={item.index} className={selected===index?'selected':''} onClick={()=>setSelected(index)}><i>{index+1}</i><span><strong>{item.name.replace(' Button','')}</strong><small>{item.haptics===0?'Haptics off':`Haptics ${item.haptics}/${data.maxHaptics}`}</small></span></button>)}</nav>
      <div className="haptic-live-readout"><div><span>SELECTED CONTROL</span><strong>{button.name}</strong></div><div><span>CLICK INTENSITY</span><strong>{button.haptics===0?'OFF':`${button.haptics} / ${data.maxHaptics}`}</strong></div></div>
    </section>
    <aside className="haptics-settings-panel">
      <header><div><span>BUTTON SETTINGS</span><h2>{button.name}</h2><p>Changes apply directly to the switch controller.</p></div><i>{selected+1}</i></header>
      <div className="haptic-settings-list"><Slider title="Click haptics" description="Strength of the physical click response" min={0} max={data.maxHaptics} value={button.haptics} onChange={v=>change('haptics',v)} onCommit={v=>save('haptics',v)}/><Slider title="Actuation point" description="How far the switch travels before activating" min={1} max={data.maxActuation} value={button.actuation} onChange={v=>change('actuation',v)} onCommit={v=>save('actuation',v)}/><Slider title="Rapid trigger" description="Reset sensitivity for repeated presses" min={1} max={data.maxRapidTrigger} value={button.rapidTrigger} onChange={v=>change('rapidTrigger',v)} onCommit={v=>save('rapidTrigger',v)}/></div>
      <footer><Icon name="haptics"/><div><strong>Global switch control</strong><span>These settings are not profile-specific.</span><InlineStatus state={operation} idle="Changes apply immediately and remain on the mouse"/></div></footer>
    </aside>
  </div>
}

function Slider({title,description,min,max,value,onChange,onCommit}: {title:string;description:string;min:number;max:number;value:number;onChange:(v:number)=>void;onCommit:(v:number)=>void}) { return <div className="haptic-setting-row"><div><span>{title}</span><small>{description}</small></div><strong>{value}</strong><input className="range" type="range" min={min} max={max} step="1" value={value} onChange={e=>onChange(+e.target.value)} onMouseUp={e=>onCommit(+(e.target as HTMLInputElement).value)} onKeyUp={e=>onCommit(+(e.target as HTMLInputElement).value)}/><div className="range-scale"><span>{min}</span><span>{max}</span></div></div> }
function Loader({text}: {text:string}) { return <div className="loader"><i/><span>{text}</span></div> }

export default function App() {
  const [page,setPage] = useState<Page>('dashboard'), [state,setState] = useState<DeviceState>({connected:false,permissionDenied:false,name:'',path:'',battery:0,charging:false,hasBattery:false,profile:'',dpiX:0,dpiY:0,pollingRate:0,configuredPollingRate:0}), [toasts,setToasts] = useState<Toast[]>([])
  const notify = useCallback((text:string,error=false) => { const id=Date.now(); setToasts(t=>[...t,{id,text,error}]); setTimeout(()=>setToasts(t=>t.filter(x=>x.id!==id)),3200) },[])
  useEffect(() => { api.state().then(setState).catch(()=>{}); const off=onDeviceUpdate(setState); return () => off?.() },[])
  const nav = useMemo(() => [{id:'dashboard' as Page,label:'Home'},{id:'dpi' as Page,label:'DPI'},{id:'profiles' as Page,label:'Profiles'},{id:'buttons' as Page,label:'Button Mapping'},{id:'haptics' as Page,label:'Haptics'}],[])
  const view = page === 'dashboard' ? <Dashboard state={state} notify={notify}/> : !state.connected ? <EmptyState state={state}/> : page === 'dpi' ? <DPIPage notify={notify}/> : page === 'profiles' ? <ProfilesPage notify={notify}/> : page === 'buttons' ? <Buttons notify={notify}/> : <HapticsPage notify={notify}/>
  const dpi = state.dpiX === state.dpiY ? state.dpiX : `${state.dpiX}/${state.dpiY}`
  return <div className="hub-shell">
    <section className="hub-workspace">
      <header className="hub-header">
        <div className="app-wordmark">SUPERSTRIKE</div>
        <div className="device-heading"><strong>{state.connected ? friendlyDeviceName(state.name) : 'PRO X2 Superstrike'}</strong></div>
        <nav className="feature-tabs">{nav.map(n=><button key={n.id} className={page===n.id?'active':''} onClick={()=>setPage(n.id)}><span>{n.label}</span></button>)}</nav>
      </header>
      <main className="content">{view}</main>
      <footer className="device-strip">
        <div className="strip-tile"><Icon name="signal"/><span>Stored rate</span><strong>{state.configuredPollingRate ? `${state.configuredPollingRate} Hz` : '—'}</strong></div>
        <div className="strip-tile"><span>Live DPI</span><strong>{state.connected ? dpi : '—'}</strong></div>
        <div className="battery-strip"><Icon name="battery"/><i><b style={{width: `${state.hasBattery ? state.battery : 0}%`}}/></i><strong>{state.hasBattery ? `${state.battery}%` : '—'}</strong></div>
        <div className="strip-state"><span className={`strip-dot ${state.connected ? 'online' : ''}`}/>{state.connected ? 'Device ready' : 'Searching'}</div>
        <div className="strip-profile"><span>Onboard profile</span><strong>{friendlyName(state.profile, '—')}</strong></div>
      </footer>
    </section>
    <div className="toast-stack">{toasts.map(t=><div key={t.id} className={`toast ${t.error?'error':''}`}><span>{t.error?'!':'✓'}</span>{t.text}</div>)}</div>
  </div>
}
