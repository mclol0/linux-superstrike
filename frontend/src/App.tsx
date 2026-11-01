import { type ReactElement, lazy, Suspense, useCallback, useEffect, useMemo, useState } from 'react'
import { api, ButtonPayload, Choices, DeviceState, DPIStage, Haptics, onDeviceUpdate, Profile } from './backend'
import productImage from './productImage'
import productSideImage from './productSideImage'
import { primaryButtonPaths } from './mouseButtonPaths'
import { sideButtonPaths } from './sideButtonPaths'
import { wheelButtonPath } from './wheelButtonPath'

const ThreeMouseViewer = lazy(() => import('./ThreeMouseViewer'))

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

function buttonAssignmentName(kind:number,code:number,mods:number,choices:Choices) {
  if(kind===0)return 'Disabled'
  const list=kind===1?choices.mouse:kind===2?choices.keys:kind===3?choices.media:choices.functions
  const name=list.find(choice=>choice.code===code)?.name||`Code ${code}`
  if(kind!==2)return name
  const modifierNames=[['Ctrl',1],['Shift',2],['Alt',4],['Super',8]].filter(([,bit])=>mods&Number(bit)).map(([label])=>label)
  return modifierNames.length?`${modifierNames.join('+')} + ${name}`:`Key: ${name}`
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

function ActuationOverlay({values,max,haptics,maxHaptics,selected,onSelect}: {values:number[];max:number;haptics:number[];maxHaptics:number;selected?:number;onSelect?:(index:number)=>void}) {
  const top=283, bottom=999, height=bottom-top
  const labels=[{name:'Left button',anchor:[1040,650],label:[410,570],edge:[830,665]},{name:'Right button',anchor:[1460,650],label:[1670,570],edge:[1670,665]}]
  return <svg className={`mouse-actuation-overlay ${onSelect?'interactive':''}`} viewBox="848 279 806 1601" role="group" aria-label="Haptic mouse buttons">
    <defs><linearGradient id="actuation-fill" x1="0" y1="1" x2="0" y2="0"><stop offset="0" stopColor="#108cff" stopOpacity=".72"/><stop offset="1" stopColor="#55c8ff" stopOpacity=".34"/></linearGradient><pattern id="actuation-hatch" width="28" height="28" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><line className="button-hatch-line" x1="0" y1="0" x2="0" y2="28"/></pattern>{primaryButtonPaths.map((path,index)=><clipPath id={`primary-button-clip-${index}`} key={index}><path d={path}/></clipPath>)}</defs>
    {primaryButtonPaths.map((path,index)=>{const value=Math.max(1,Math.min(max,values[index]||1)),ratio=max<=1?1:1-(value-1)/(max-1),strength=Math.max(0,Math.min(1,(haptics[index]||0)/Math.max(1,maxHaptics))),y=bottom-height*ratio,opacity=Math.min(.92,.14+strength*.62+(selected===index?.14:0)),label=labels[index],centerX=label.label[0]+210;return <g key={index} role="button" tabIndex={0} aria-label={`Configure ${label.name}`} className={`haptic-vector-group ${selected===index?'selected':''}`} onClick={()=>onSelect?.(index)} onKeyDown={event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();onSelect?.(index)}}}><path className="actuation-button-outline" d={path} style={{filter:`drop-shadow(0 0 ${2+strength*8}px rgba(16,140,255,${.08+strength*.4}))`}}/><rect className="actuation-button-fill" x={index===0?848:1250} y={y} width="406" height={height*ratio} opacity={opacity} clipPath={`url(#primary-button-clip-${index})`}/><rect className="actuation-button-hatch" x={index===0?848:1250} y={y} width="406" height={height*ratio} style={{opacity:.2+strength*.42}} clipPath={`url(#primary-button-clip-${index})`}/><line className="actuation-point-line" x1={index===0?848:1250} x2={index===0?1250:1656} y1={y} y2={y} clipPath={`url(#primary-button-clip-${index})`}/><path className="actuation-button-hit" d={path}/><g className="haptic-control-callout"><line x1={label.anchor[0]} y1={label.anchor[1]} x2={label.edge[0]} y2={label.edge[1]}/><circle cx={label.anchor[0]} cy={label.anchor[1]} r="12"/><rect x={label.label[0]} y={label.label[1]} width="420" height="180"/><text className="title" textAnchor="middle" x={centerX} y={label.label[1]+58}>{label.name}</text><text className="detail" textAnchor="middle" x={centerX} y={label.label[1]+108}>ACTUATION <tspan>{value} / {max}</tspan></text><text className="detail" textAnchor="middle" x={centerX} y={label.label[1]+151}>HAPTICS <tspan>{haptics[index]===0?'OFF':`${haptics[index]} / ${maxHaptics}`}</tspan></text></g></g>})}
  </svg>
}

function TopControlOverlay({selected,onSelect,assignments=[]}: {selected?:number;onSelect?:(index:number)=>void;assignments?:string[]}) {
  const controls=[
    {index:0,name:'Left button',path:primaryButtonPaths[0],anchor:[1040,650],label:[400,570],edge:[820,660],width:420},
    {index:1,name:'Right button',path:primaryButtonPaths[1],anchor:[1460,650],label:[1680,570],edge:[1680,660],width:420},
    {index:2,name:'Middle button',path:wheelButtonPath.path,transform:wheelButtonPath.transform,anchor:[1250,650],label:[990,1040],edge:[1250,1040],width:520},
  ]
  return <svg className="top-control-overlay" viewBox="848 279 806 1601" role="group" aria-label="Mouse buttons">
    <defs><pattern id="top-control-hatch" width="28" height="28" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><line className="button-hatch-line" x1="0" y1="0" x2="0" y2="28"/></pattern></defs>
    {controls.map(control=><g key={control.index} role="button" tabIndex={0} aria-label={`Select ${control.name}`} className={`control-vector-group ${selected===control.index?'selected':''}`} onClick={()=>onSelect?.(control.index)} onKeyDown={event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();onSelect?.(control.index)}}}>
      <path d={control.path} transform={control.transform} className={`top-control-vector ${control.index===2?'wheel':''}`}/>
      <path d={control.path} transform={control.transform} className="control-vector-hatch"/>
      <g className="control-callout mapping"><line x1={control.anchor[0]} y1={control.anchor[1]} x2={control.edge[0]} y2={control.edge[1]}/><circle cx={control.anchor[0]} cy={control.anchor[1]} r="12"/><rect x={control.label[0]} y={control.label[1]} width={control.width} height="180"/><text className="title" textAnchor="middle" x={control.label[0]+control.width/2} y={control.label[1]+58}>{control.name}</text><text className="kicker" textAnchor="middle" x={control.label[0]+control.width/2} y={control.label[1]+105}>CURRENT MAPPING</text><text className="assignment" textAnchor="middle" x={control.label[0]+control.width/2} y={control.label[1]+150} textLength={(assignments[control.index]||'Unassigned').length>20?control.width-52:undefined} lengthAdjust="spacingAndGlyphs">{assignments[control.index]||'Unassigned'}</text></g>
    </g>)}
  </svg>
}

function MouseArt({interactive = false, selected, onSelect, visibleMarkers=[0,1,2], actuation, mappingControls=false, mappingAssignments=[]}: {interactive?: boolean; selected?: number; onSelect?: (i: number) => void; visibleMarkers?:number[];actuation?:{values:number[];max:number;haptics:number[];maxHaptics:number};mappingControls?:boolean;mappingAssignments?:string[]}) {
  const marker = (index: number, x: number, y: number) => <g onClick={() => onSelect?.(index)} className={`mouse-marker ${selected === index ? 'selected' : ''}`}><circle cx={x} cy={y} r="30"/><text x={x} y={y + 2}>{index + 1}</text></g>
  return <div className={`mouse-art ${interactive ? 'interactive' : ''}`} role="img" aria-label="PRO X2 Superstrike mouse">
    <img src={productImage} alt="" draggable="false"/>
    {actuation&&<ActuationOverlay values={actuation.values} max={actuation.max} haptics={actuation.haptics} maxHaptics={actuation.maxHaptics} selected={selected} onSelect={onSelect}/>}
    {mappingControls&&<TopControlOverlay selected={selected} onSelect={onSelect} assignments={mappingAssignments}/>}
    {interactive && !mappingControls && visibleMarkers.length>0 && <svg className="mouse-hitareas" viewBox="0 0 806 1601" aria-hidden="true">
      {visibleMarkers.includes(0)&&marker(0, 285, 300)}
      {visibleMarkers.includes(1)&&marker(1, 520, 300)}
      {visibleMarkers.includes(2)&&marker(2, 403, 335)}
    </svg>}
  </div>
}

function MouseSideArt({interactive=false,selected,onSelect,assignments=[]}: {interactive?:boolean;selected?:number;onSelect?:(index:number)=>void;assignments?:string[]}) {
  const controls=[
    {index:4,name:'Button 5',...sideButtonPaths[0],anchor:[440,552],label:[150,600],edge:[420,640]},
    {index:3,name:'Button 4',...sideButtonPaths[1],anchor:[595,535],label:[700,420],edge:[700,463]},
  ]
  return <><img className="mouse-side-art" src={productSideImage} alt="PRO X2 Superstrike side view" draggable="false"/>{interactive&&<svg className="side-button-overlay" viewBox="70 399 974 311" role="group" aria-label="Side mouse buttons"><defs><pattern id="side-control-hatch" width="12" height="12" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><line className="button-hatch-line compact" x1="0" y1="0" x2="0" y2="12"/></pattern></defs>{controls.map(control=>{const assignment=assignments[control.index]||'Unassigned',centerX=control.label[0]+135;return <g key={control.index} role="button" tabIndex={0} aria-label={`Select ${control.name}`} className={`control-vector-group ${selected===control.index?'selected':''}`} onClick={()=>onSelect?.(control.index)} onKeyDown={event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();onSelect?.(control.index)}}}><path d={control.path} transform={control.transform} className="side-button-vector"/><path d={control.path} transform={control.transform} className="control-vector-hatch side"/><g className="control-callout compact mapping"><line x1={control.anchor[0]} y1={control.anchor[1]} x2={control.edge[0]} y2={control.edge[1]}/><circle cx={control.anchor[0]} cy={control.anchor[1]} r="5"/><rect x={control.label[0]} y={control.label[1]} width="270" height="86"/><text className="title" textAnchor="middle" x={centerX} y={control.label[1]+27}>{control.name}</text><text className="kicker" textAnchor="middle" x={centerX} y={control.label[1]+49}>MAPPED TO</text><text className="assignment" textAnchor="middle" x={centerX} y={control.label[1]+72} textLength={assignment.length>18?240:undefined} lengthAdjust="spacingAndGlyphs">{assignment}</text></g></g>})}</svg>}</>
}

function EmptyState({state,preparing=false}: {state: DeviceState;preparing?:boolean}) {
  const denied=!preparing&&state.permissionDenied
  return <div className={`device-wait-screen ${denied?'permission':''}`}>
    <section className="wait-copy-panel">
      <span className="settings-kicker">{preparing?'STARTING SUPERSTRIKE':'DEVICE CONNECTION'}</span>
      <h1>{preparing?'Preparing your mouse':denied?'Access required':'Connect your Superstrike'}</h1>
      <p>{preparing?'Your device is ready. Superstrike Control is finishing the interactive product view before opening Home.':denied?'The mouse is visible to Linux, but Superstrike Control cannot open it yet. Install the included device-access rule, then reconnect the mouse.':'Power on the mouse and connect it by cable or LIGHTSPEED receiver. The app will open your settings automatically when the device responds.'}</p>
      <div className={`wait-status ${denied?'blocked':''}`}><i/><div><strong>{preparing?'Loading high-detail product view':denied?'Device found · access blocked':'Searching for PRO X2 Superstrike'}</strong><small>{preparing?'This only happens once when the app starts':denied?'Reconnect after granting access':'Automatic detection is running'}</small></div></div>
      <div className="wait-checklist">
        <div><b>01</b><span><strong>Power on the mouse</strong><small>Use wired or LIGHTSPEED mode</small></span></div>
        <div><b>02</b><span><strong>Connect to this computer</strong><small>Direct USB connections work too</small></span></div>
        <div><b>03</b><span><strong>Keep this window open</strong><small>No refresh or restart is required</small></span></div>
      </div>
    </section>
    <section className="wait-product-panel"><span>DEVICE PREVIEW</span><MouseArt/><div><strong>PRO X2 Superstrike</strong><small>HID++ 4.2 gaming mouse</small></div></section>
    <footer className="wait-footer"><span className={`strip-dot ${denied?'warning':''}`}/><strong>{preparing?'Starting application':denied?'Permission setup required':'Waiting for device'}</strong><small>{preparing?'Home will open automatically when ready.':denied?'Run the included installer if the access rule is missing.':'This page updates as soon as the mouse is available.'}</small></footer>
  </div>
}

function Metric({label, value, suffix, detail, icon}: {label: string; value: string | number; suffix?: string; detail: string; icon: 'profiles'|'signal'|'battery'}) {
  return <article className="metric glass"><div className="metric-top"><span>{label}</span><Icon name={icon}/></div><div className="metric-value">{value}<small>{suffix}</small></div><p>{detail}</p></article>
}

function Dashboard({state, notify, active=true, onModelPrepared}: {state: DeviceState; notify: (s: string, e?: boolean) => void; active?:boolean;onModelPrepared?:()=>void}) {
  const [selectedRate, setSelectedRate] = useState(0)
  const [activeSector, setActiveSector] = useState(0)
  const [savingRate, setSavingRate] = useState(false)
  const [operation,setOperation] = useState<OperationState>()
  useEffect(() => {
    if (!state.connected) return
    api.profiles().then(profiles => {
      const active = profiles.find(profile => profile.active)
      if (active) { setSelectedRate(active.pollingRate); setActiveSector(active.sector) }
    }).catch(() => {})
  }, [state.connected, state.profile])
  const chooseRate = async (rate: number) => {
    if (savingRate || rate === selectedRate) return
    const previous = selectedRate
    setSelectedRate(rate); setSavingRate(true); setOperation({text:'Writing polling rate…',tone:'saving'})
    try {
      let sector=activeSector
      if(!sector){const profiles=await api.profiles();const active=profiles.find(profile=>profile.active);if(!active)throw new Error('No active onboard profile was found');sector=active.sector;setActiveSector(sector)}
      const actualSector=await api.updateRate(sector, rate)
      setActiveSector(actualSector)
      setOperation({text:`${rate} Hz stored in the active profile`,tone:'saved'})
      notify(`Polling rate set to ${rate} Hz`)
    } catch (error) {
      setSelectedRate(previous); setOperation({text:'Polling-rate write failed',tone:'error'}); notify(String(error), true)
    } finally { setSavingRate(false) }
  }
  return <div className="hub-dashboard page-enter">
    <section className="hub-panel three-product-view"><Suspense fallback={null}><ThreeMouseViewer active={active} onPrepared={onModelPrepared}/></Suspense></section>
    <section className="hub-panel rate-overview"><div><h2>Stored polling rate</h2><p>Select the USB report frequency for the active onboard profile.</p><InlineStatus state={operation} idle="Stored in the active profile"/></div><div className={`rate-track ${savingRate ? 'saving' : ''}`}>{rates.slice().reverse().map(rate=><button key={rate} className={selectedRate===rate?'active':''} onClick={()=>chooseRate(rate)} disabled={savingRate}>{rate}<small>Hz</small></button>)}</div></section>
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
  const save=async(stage:DPIStage,enabled=stage.enabled)=>{
    if(!activeProfile||saving!==undefined)return
    const x=Math.max(100,Math.min(44000,stage.x)),y=Math.max(100,Math.min(44000,stage.y)),updatedStage={...stage,x,y,enabled}
    const previousLiveIndex=activeProfile.currentDpiStage>=0?activeProfile.currentDpiStage:activeProfile.defaultDpiStage
    const makeDefault=enabled&&stage.index===activeProfile.defaultDpiStage
    setSaving(stage.index);setOperation({text:`Writing slot ${stage.index+1}…`,tone:'saving'})
    try{
      const actualSector=await api.updateDPIStage(activeProfile.sector,stage.index,x,y,enabled,makeDefault)
      const nextStages=drafts.map(item=>item.index===stage.index?updatedStage:item)
      const liveStage=nextStages.find(item=>item.index===previousLiveIndex&&item.enabled)||nextStages.find(item=>item.index===activeProfile.defaultDpiStage&&item.enabled)||nextStages.find(item=>item.enabled)
      if(liveStage&&!(makeDefault&&liveStage.index===stage.index))await api.selectDPI(actualSector,liveStage.x)
      setDrafts(nextStages);if(liveStage)setSelected(liveStage.index)
      setActiveProfile(current=>current?{...current,sector:actualSector,dpiStages:nextStages,currentDpiStage:liveStage?.index??current.currentDpiStage,dpiX:liveStage?.x||current.dpiX,dpiY:liveStage?.y||current.dpiY}:current)
      setOperation({text:`Slot ${stage.index+1} stored onboard`,tone:'saved'});notify(`DPI slot ${stage.index+1} saved to the mouse`)
    }catch(error){setOperation({text:'DPI write failed',tone:'error'});notify(String(error),true);load()}finally{setSaving(undefined)}
  }
  const selectStage=async(stage:DPIStage)=>{if(!activeProfile||saving!==undefined||!stage.enabled)return;setSaving(stage.index);setSelected(stage.index);setOperation({text:`Selecting slot ${stage.index+1} and storing it onboard…`,tone:'saving'});try{const actualSector=await api.updateDPIStage(activeProfile.sector,stage.index,stage.x,stage.y,true,true);setActiveProfile(current=>current?{...current,sector:actualSector,defaultDpiStage:stage.index,currentDpiStage:stage.index,dpiX:stage.x,dpiY:stage.y}:current);setOperation({text:`Slot ${stage.index+1} is active and stored as default`,tone:'saved'});notify(`DPI slot ${stage.index+1} selected and stored onboard`)}catch(error){setOperation({text:'Could not select the DPI slot',tone:'error'});notify(String(error),true);load()}finally{setSaving(undefined)}}
  if(busy&&!activeProfile)return <Loader text="Reading active profile"/>
  if(!activeProfile)return <EmptyState state={{connected:false,permissionDenied:false} as DeviceState}/>
  if(!activeProfile.hasDpiStages)return <div className="dpi-unsupported"><h1>DPI stages unavailable</h1><p>This onboard profile uses an older layout that does not safely expose five independent stages.</p></div>
  return <div className="dpi-hub-page page-enter">
    <main className="dpi-work-panel">
      <header className="dpi-title"><div><h1>DPI</h1><p>Five sensitivity slots stored directly on the mouse.</p></div><div className="dpi-title-actions"><strong>{drafts.filter(stage=>stage.enabled).length} / 5 enabled</strong></div></header>
      <div className="dpi-stage-head"><span>Slot</span><span>DPI</span><span>Enabled</span></div>
      <div className="dpi-stage-list">{drafts.map(stage=><div key={stage.index} className={`dpi-stage-row onboard ${selected===stage.index?'selected':''} ${!stage.enabled?'disabled':''}`} onClick={()=>setSelected(stage.index)}>
        <button className={`dpi-active-radio ${activeProfile.currentDpiStage===stage.index?'active':''}`} title="Select this DPI and store it as the onboard default" disabled={!stage.enabled||saving!==undefined} onClick={event=>{event.stopPropagation();selectStage(stage)}} />
        <span className="dpi-stage-index" style={{borderColor:dpiColors[stage.index]}}>{stage.index+1}</span>
        <input className="dpi-stage-range" type="range" min="100" max="44000" step="50" disabled={!stage.enabled||saving!==undefined} value={stage.x} onChange={event=>edit(stage.index,+event.target.value)} onPointerUp={event=>{const value=+event.currentTarget.value;save({...stage,x:value,y:value})}} />
        <input className="dpi-stage-number" type="number" min="100" max="44000" step="50" disabled={!stage.enabled||saving!==undefined} value={stage.x} onChange={event=>edit(stage.index,+event.target.value)} onBlur={()=>save(stage)} onKeyDown={event=>{if(event.key==='Enter')save(stage)}}/>
        <span className="dpi-unit">DPI</span>
        <button className={`switch dpi-stage-switch ${stage.enabled?'on':''}`} disabled={saving!==undefined} onClick={event=>{event.stopPropagation();save(stage,!stage.enabled)}}><i/></button>
      </div>)}</div>
      <div className="dpi-save-note"><p>The filled circle is the active DPI and the slot restored from onboard memory. Mouse DPI controls cycle through enabled slots.</p><InlineStatus state={operation} idle="Changes are stored directly on the mouse"/></div>
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
  const action = async(label:string,fn:()=>Promise<unknown>,apply:(result:unknown)=>void)=>{setOperation({text:'Updating onboard profile…',tone:'saving'});try{const result=await fn();apply(result);setOperation({text:label,tone:'saved'});notify(label)}catch(error){setOperation({text:'Could not update the profile',tone:'error'});notify(String(error),true);load()}}
  if(busy&&!profiles.length)return <Loader text="Reading onboard profiles"/>
  if(!profile)return <EmptyState state={{connected:false,permissionDenied:false} as DeviceState}/>
  const defaultStage=profile.dpiStages.find(stage=>stage.index===profile.defaultDpiStage)
  return <div className="profiles-manager page-enter">
    <aside className="profile-browser">
      <header><div><span>ONBOARD MEMORY</span><h1>Profiles</h1></div><button className="icon-button" onClick={load} aria-label="Refresh profiles" data-tooltip="Refresh profiles"><Icon name="refresh"/></button></header>
      <div className="profile-browser-list">{profiles.map((item,index)=><button key={item.sector} className={`${selected===index?'selected':''} ${item.active?'active':''} ${!item.enabled?'disabled':''}`} onClick={()=>{setSelected(index);setOperation(undefined)}}><span className="browser-index">{String(index+1).padStart(2,'0')}</span><span><strong>{friendlyName(item.name,`Profile ${item.index}`)}</strong><small>{item.enabled?'Stored onboard':'Disabled'} · {item.active?'Active':`${item.pollingRate} Hz`}</small></span><i/></button>)}</div>
      <footer><span>{profiles.length} onboard profiles</span><strong>{friendlyName(profiles.find(item=>item.active)?.name,'None')} active</strong></footer>
    </aside>
    <main className="profile-settings-panel profile-settings-wide">
      <header className="profile-summary-header"><div><span>PROFILE {String(profile.index).padStart(2,'0')}</span><h1>{friendlyName(profile.name,`Profile ${profile.index}`)}</h1><p>{profile.active?'Currently active on the mouse':'Stored in onboard memory'}</p></div><div className={`profile-live-badge ${profile.active?'active':''}`}><i/>{profile.active?'Active':'Stored profile'}</div><section className="profile-summary-stats"><div><span>Default DPI</span><strong>{defaultStage?.x||profile.dpiX}</strong></div><div><span>Stored polling</span><strong>{profile.pollingRate} Hz</strong></div><div><span>DPI slots</span><strong>{profile.dpiStages.filter(stage=>stage.enabled).length} enabled</strong></div><div><span>Onboard state</span><strong>{profile.enabled?'Enabled':'Disabled'}</strong></div></section></header>
      <div className="profile-detail-body">
        <div className="profile-overview-grid"><section className="profile-detail-card"><span className="settings-kicker">IDENTITY</span><h2>Profile name</h2><p className="section-description">The name shown when managing this onboard slot.</p><label>Name</label><input value={name} maxLength={24} onChange={event=>setName(event.target.value)}/><button className="button subtle full" disabled={!name.trim()||name.trim()===shownName} onClick={()=>action('Profile renamed',()=>api.rename(profile.sector,name.trim()),result=>setProfiles(current=>current.map((item,index)=>index===selected?{...item,sector:Number(result),name:name.trim()}:item)))}>Save name</button></section><section className="profile-detail-card"><span className="settings-kicker">ONBOARD BEHAVIOR</span><h2>Profile state</h2><p className="section-description">Control whether this profile is available and active.</p><div className="profile-setting-line"><div><strong>Enabled</strong><small>Available when cycling profiles on the mouse</small></div><button className={`switch ${profile.enabled?'on':''}`} onClick={()=>action(profile.enabled?'Profile disabled':'Profile enabled',()=>api.enable(profile.index,!profile.enabled),()=>setProfiles(current=>current.map((item,index)=>index===selected?{...item,enabled:!profile.enabled}:item)))}><i/></button></div><div className="profile-setting-line"><div><strong>Live profile</strong><small>{profile.active?'This profile is active':'Another profile is currently in use'}</small></div><span className={`profile-state-dot ${profile.active?'on':''}`}/></div>{!profile.active&&<button className="button primary full" disabled={!profile.enabled} onClick={()=>action('Profile activated',()=>api.activate(profile.sector),()=>setProfiles(current=>current.map((item,index)=>({...item,active:index===selected}))))}>Use this profile</button>}</section></div>
        <div className="profile-data-grid"><section className="profile-detail-card profile-dpi-summary"><span className="settings-kicker">STORED DPI</span><h2>DPI slots</h2><p className="section-description">The five sensitivity stages saved in this profile.</p>{profile.hasDpiStages?<div className="profile-dpi-list">{profile.dpiStages.map(stage=><div key={stage.index} className={!stage.enabled?'disabled':''}><i style={{background:dpiColors[stage.index]}}/><span>Slot {stage.index+1}</span><strong>{stage.x===stage.y?stage.x:`${stage.x} × ${stage.y}`} DPI</strong>{profile.active&&(profile.currentDpiStage>=0?profile.currentDpiStage:profile.defaultDpiStage)===stage.index?<em className="active">Active</em>:profile.defaultDpiStage===stage.index&&<em>Default</em>}<small>{stage.enabled?'Enabled':'Disabled'}</small></div>)}</div>:<p className="profile-dpi-unavailable">Independent DPI slots aren’t exposed by this profile layout.</p>}</section><section className="profile-detail-card profile-button-summary"><span className="settings-kicker">STORED CONTROLS</span><h2>Button mappings</h2><p className="section-description">Assignments saved in this onboard profile.</p><div className="profile-button-list">{profile.buttonMappings.map(button=><div key={button.index}><i>{button.index+1}</i><span>{button.name}</span><strong>{button.assignment}</strong></div>)}</div></section></div>
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
  const save=async()=>{setSaving(true);setOperation({text:'Writing assignment…',tone:'saving'});try{const actualSector=await api.setButton(data.sector,item.index,kind,code,mods),description=buttonAssignmentName(kind,code,mods,choices),action={Kind:kind,Code:code,Mods:mods,Raw:item.action.Raw};setData(current=>current?{...current,sector:actualSector,buttons:current.buttons.map((button,index)=>index===selected?{...button,description,action}:button)}:current);setOperation({text:`${item.name} stored in this profile`,tone:'saved'});notify(`${item.name} remapped`)}catch(error){setOperation({text:'Button assignment failed',tone:'error'});notify(String(error),true);load()}finally{setSaving(false)}}
  return <div className="button-mapper page-enter">
    <section className="button-visual-panel">
      <div className="button-top-view"><MouseArt interactive mappingControls mappingAssignments={data.buttons.map(button=>button.description)} selected={selected} onSelect={index=>{if(data.buttons[index])setSelected(index)}}/></div>
      <div className="button-side-view"><MouseSideArt interactive assignments={data.buttons.map(button=>button.description)} selected={selected} onSelect={index=>setSelected(index)}/></div>
    </section>
    <aside className="mapping-editor-panel">
      <header><div><span>SELECTED CONTROL</span><h1>{item.name}</h1><p>{item.description}</p></div></header>
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
  const load = useCallback(async () => { try { setData(await api.haptics()) } catch(e) { notify(String(e), true) } }, [notify]); useEffect(() => { load() }, [load])
  const change = (field: 'haptics'|'actuation'|'rapidTrigger', value: number) => setData(d => d ? {...d,buttons:d.buttons.map((b,i)=>i===selected?{...b,[field]:value}:b)} : d)
  const save = async (field: string, value: number) => { try { await api.setHaptic(selected,field,value);notify('Haptic setting saved') } catch(e) { notify(String(e),true);load() } }
  const button = data?.buttons[selected]
  if (!data || !button) return <Loader text="Reading analog switch settings"/>
  return <div className="haptics-mapper page-enter">
    <section className="haptics-device-panel">
      <div className="haptics-mouse"><MouseArt interactive selected={selected} visibleMarkers={[]} actuation={{values:data.buttons.map(item=>item.actuation),max:data.maxActuation,haptics:data.buttons.map(item=>item.haptics),maxHaptics:data.maxHaptics}} onSelect={index=>setSelected(index)}/></div>
    </section>
    <aside className="haptics-settings-panel">
      <header><div><span>BUTTON SETTINGS</span><h2>{button.name}</h2><p>Changes apply directly to the switch controller.</p></div></header>
      <div className="haptic-settings-list"><Slider title="Click haptics" description="Strength of the physical click response" min={0} max={data.maxHaptics} value={button.haptics} onChange={v=>change('haptics',v)} onCommit={v=>save('haptics',v)}/><Slider title="Actuation point" description="How far the switch travels before activating" min={1} max={data.maxActuation} value={button.actuation} onChange={v=>change('actuation',v)} onCommit={v=>save('actuation',v)}/><Slider title="Rapid trigger" description="Reset sensitivity for repeated presses" min={1} max={data.maxRapidTrigger} value={button.rapidTrigger} onChange={v=>change('rapidTrigger',v)} onCommit={v=>save('rapidTrigger',v)}/></div>
    </aside>
  </div>
}

function Slider({title,description,min,max,value,onChange,onCommit}: {title:string;description:string;min:number;max:number;value:number;onChange:(v:number)=>void;onCommit:(v:number)=>void}) { return <div className="haptic-setting-row"><div><span>{title}</span><small>{description}</small></div><strong key={value} className="animated-setting-value">{value}</strong><input className="range" type="range" min={min} max={max} step="1" value={value} onChange={e=>onChange(+e.target.value)} onMouseUp={e=>onCommit(+(e.target as HTMLInputElement).value)} onKeyUp={e=>onCommit(+(e.target as HTMLInputElement).value)}/><div className="range-scale"><span>{min}</span><span>{max}</span></div></div> }
function Loader({text}: {text:string}) { return <div className="loader"><i/><span>{text}</span></div> }

export default function App() {
  const [page,setPage] = useState<Page>('dashboard'), [state,setState] = useState<DeviceState>({connected:false,permissionDenied:false,name:'',path:'',battery:0,charging:false,hasBattery:false,profile:'',dpiX:0,dpiY:0,pollingRate:0,configuredPollingRate:0}), [toasts,setToasts] = useState<Toast[]>([]), [modelPrepared,setModelPrepared] = useState(false)
  const notify = useCallback((text:string,error=false) => { const id=Date.now(); setToasts(t=>[...t,{id,text,error}]); setTimeout(()=>setToasts(t=>t.filter(x=>x.id!==id)),3200) },[])
  const markModelPrepared = useCallback(() => setModelPrepared(true), [])
  useEffect(() => { api.state().then(setState).catch(()=>{}); const off=onDeviceUpdate(setState); return () => off?.() },[])
  const nav = useMemo(() => [{id:'dashboard' as Page,label:'Home'},{id:'dpi' as Page,label:'DPI'},{id:'profiles' as Page,label:'Profiles'},{id:'buttons' as Page,label:'Button Mapping'},{id:'haptics' as Page,label:'Haptics'}],[])
  const view = !state.connected ? <EmptyState state={state}/> : page === 'dpi' ? <DPIPage notify={notify}/> : page === 'profiles' ? <ProfilesPage notify={notify}/> : page === 'buttons' ? <Buttons notify={notify}/> : <HapticsPage notify={notify}/>
  const dpi = state.dpiX === state.dpiY ? state.dpiX : `${state.dpiX}/${state.dpiY}`
  return <div className="hub-shell">
    <section className="hub-workspace">
      <header className="hub-header">
        <div className="app-wordmark">SUPERSTRIKE</div>
        <div className="device-heading"><strong>{state.connected ? friendlyDeviceName(state.name) : 'PRO X2 Superstrike'}</strong></div>
        <nav className="feature-tabs">{nav.map(n=><button key={n.id} className={page===n.id?'active':''} onClick={()=>setPage(n.id)}><span>{n.label}</span></button>)}</nav>
      </header>
      <main className="content">
        <div className={`persistent-home ${state.connected && modelPrepared && page === 'dashboard' ? 'visible' : 'hidden'}`}><Dashboard state={state} notify={notify} active={state.connected && modelPrepared && page === 'dashboard'} onModelPrepared={markModelPrepared}/></div>
        {page === 'dashboard' ? (!state.connected || !modelPrepared) && <EmptyState state={state} preparing={state.connected && !modelPrepared}/> : view}
      </main>
      <footer className="device-strip">
        <div className="strip-tile"><Icon name="signal"/><span>Polling rate</span><strong>{state.configuredPollingRate ? `${state.configuredPollingRate} Hz` : '—'}</strong></div>
        <div className="strip-tile"><span>DPI</span><strong>{state.connected ? dpi : '—'}</strong></div>
        <div className="battery-strip"><Icon name="battery"/><i><b style={{width: `${state.hasBattery ? state.battery : 0}%`}}/></i><strong>{state.hasBattery ? `${state.battery}%` : '—'}</strong></div>
        <div className="strip-state"><span className={`strip-dot ${state.connected ? 'online' : ''}`}/>{state.connected ? 'Device connected' : 'Searching'}</div>
        <div className="strip-profile"><span>Current profile</span><strong>{friendlyName(state.profile, '—')}</strong></div>
      </footer>
    </section>
    <div className="toast-stack">{toasts.map(t=><div key={t.id} className={`toast ${t.error?'error':''}`}><span>{t.error?'!':'✓'}</span>{t.text}</div>)}</div>
  </div>
}
