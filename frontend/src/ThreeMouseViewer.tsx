import { useEffect, useRef, useState } from 'react'
import * as THREE from 'three'
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js'
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js'
import { MeshoptDecoder } from 'three/examples/jsm/libs/meshopt_decoder.module.js'
import { RoomEnvironment } from 'three/examples/jsm/environments/RoomEnvironment.js'
import modelUrl from './assets/model/SUPERSTRIKE.glb?url'
import atlasTextureUrl from './assets/model/SUPERSTRIKE_ATLAS.jpg?url'

type ViewerState = 'loading' | 'ready' | 'error'

export default function ThreeMouseViewer({active = true, onPrepared}: {active?: boolean;onPrepared?:()=>void}) {
  const host = useRef<HTMLDivElement>(null)
  const activeRef = useRef(active)
  const [state, setState] = useState<ViewerState>('loading')

  useEffect(() => { activeRef.current = active }, [active])

  useEffect(() => {
    const container = host.current
    if (!container) return

    let frame = 0
    let disposed = false
    const scene = new THREE.Scene()
    const camera = new THREE.PerspectiveCamera(31, 1, .01, 100)
    camera.position.set(3.2, 2.15, 3.7)

    let renderer: THREE.WebGLRenderer
    try {
      renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, powerPreference: 'high-performance' })
    } catch {
      setState('error')
      onPrepared?.()
      return
    }
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
    renderer.outputColorSpace = THREE.SRGBColorSpace
    renderer.toneMapping = THREE.ACESFilmicToneMapping
    renderer.toneMappingExposure = 1.02
    renderer.domElement.setAttribute('aria-label', 'Interactive 3D model of the PRO X2 Superstrike')
    container.appendChild(renderer.domElement)

    const controls = new OrbitControls(camera, renderer.domElement)
    controls.enableDamping = true
    controls.dampingFactor = .055
    controls.enablePan = false
    controls.minDistance = 3.2
    controls.maxDistance = 7.2
    controls.autoRotate = true
    controls.autoRotateSpeed = .72
    controls.target.set(0, .02, 0)

    const pmrem = new THREE.PMREMGenerator(renderer)
    pmrem.compileEquirectangularShader()
    const environmentTarget = pmrem.fromScene(new RoomEnvironment(), .045)
    const studioEnvironment = environmentTarget.texture
    pmrem.dispose()

    scene.add(new THREE.HemisphereLight(0xf5f7fa, 0x202226, 1.5))
    const key = new THREE.DirectionalLight(0xfffdf8, 3.45)
    key.position.set(-4.5, 5.5, 4.8); scene.add(key)
    const fill = new THREE.DirectionalLight(0xe8f0f7, 1.45)
    fill.position.set(4.2, 2.8, 4.5); scene.add(fill)
    const rim = new THREE.DirectionalLight(0xf3f6f8, 1.7)
    rim.position.set(3.5, 3.8, -4.5); scene.add(rim)
    const top = new THREE.DirectionalLight(0xffffff, .85)
    top.position.set(-.5, 7, -.5); scene.add(top)

    const manager = new THREE.LoadingManager()
    let modelLoaded = false
    manager.onLoad = () => {
      if (!disposed && modelLoaded) { setState('ready'); onPrepared?.() }
    }
    const textureLoader = new THREE.TextureLoader(manager)
    const atlasTexture = textureLoader.load(atlasTextureUrl)
    atlasTexture.colorSpace = THREE.SRGBColorSpace
    atlasTexture.flipY = true
    atlasTexture.wrapS = THREE.ClampToEdgeWrapping
    atlasTexture.wrapT = THREE.ClampToEdgeWrapping
    atlasTexture.anisotropy = renderer.capabilities.getMaxAnisotropy()
    const loader = new GLTFLoader(manager).setMeshoptDecoder(MeshoptDecoder)
    loader.load(modelUrl, gltf => {
      if (disposed) return
      const object = gltf.scene
      const bounds = new THREE.Box3().setFromObject(object)
      const size = bounds.getSize(new THREE.Vector3())
      const center = bounds.getCenter(new THREE.Vector3())
      const scale = 2.7 / Math.max(size.x, size.y, size.z)
      object.position.copy(center.multiplyScalar(-1))
      const model = new THREE.Group()
      model.scale.setScalar(scale)
      model.rotation.y = Math.PI * .72
      model.add(object)
      model.traverse(child => {
        if (!(child instanceof THREE.Mesh)) return
        const materials = Array.isArray(child.material) ? child.material : [child.material]
        for (const material of materials) {
          const textured = material as THREE.MeshPhongMaterial
          const materialName = material.name.toLowerCase()
          const atlasSlot = materialName.includes('bottompart') ? 0 : materialName.includes('click') ? 1 : materialName.includes('mainbody') ? 2 : -1
          if (atlasSlot >= 0) {
            textured.map = atlasTexture
            const uv = child.geometry.getAttribute('uv')
            for (let index = 0; index < uv.count; index++) uv.setX(index, (uv.getX(index) + atlasSlot) / 3)
            uv.needsUpdate = true
          }
          if (textured.map) {
            textured.color.set(0xffffff)
            textured.map.colorSpace = THREE.SRGBColorSpace
            textured.map.anisotropy = renderer.capabilities.getMaxAnisotropy()
          }
          textured.envMap = studioEnvironment
          textured.reflectivity = materialName.includes('click') || materialName.includes('bottompart') ? .2 : .12
          if ('shininess' in textured) textured.shininess = Math.min(textured.shininess || 28, 42)
          textured.needsUpdate = true
        }
      })
      scene.add(model)
      modelLoaded = true
    }, undefined, () => {
      if (!disposed) { setState('error'); onPrepared?.() }
    })

    const resize = () => {
      const width = Math.max(1, container.clientWidth), height = Math.max(1, container.clientHeight)
      camera.aspect = width / height; camera.updateProjectionMatrix(); renderer.setSize(width, height, false)
    }
    const observer = new ResizeObserver(resize)
    observer.observe(container); resize()

    const animate = () => {
      if (activeRef.current) { controls.update(); renderer.render(scene, camera) }
      frame = requestAnimationFrame(animate)
    }
    animate()

    return () => {
      disposed = true; cancelAnimationFrame(frame); observer.disconnect(); controls.dispose()
      scene.traverse(child => {
        if (!(child instanceof THREE.Mesh)) return
        child.geometry.dispose()
        const materials = Array.isArray(child.material) ? child.material : [child.material]
        materials.forEach(material => {
          const map = (material as THREE.MeshStandardMaterial).map
          map?.dispose(); material.dispose()
        })
      })
      environmentTarget.dispose()
      renderer.dispose(); renderer.domElement.remove()
    }
  }, [])

  return <div className={`three-mouse-viewer ${state}`} ref={host}>
    <div className="three-stage-copy"><span>INTERACTIVE VIEW</span><strong>Drag to rotate · Scroll to zoom</strong></div>
    {state === 'error' && <div className="three-stage-status error"><span>3D preview unavailable</span></div>}
  </div>
}
