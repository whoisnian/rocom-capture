import React from 'react'
import { createRoot } from 'react-dom/client'
import { HashRouter, Routes, Route, Navigate } from 'react-router-dom'
import App from './App'
import PetList from './pages/PetList'
import Events from './pages/Events'
import PetDetail from './pages/PetDetail'
import Debug from './pages/Debug'
import MapPage from './pages/Map'
import './index.css'

createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <HashRouter>
      <Routes>
        <Route element={<App />}>
          <Route index element={<Navigate to="/pets" replace />} />
          <Route path="pets" element={<PetList />} />
          <Route path="pets/:gid" element={<PetDetail />} />
          <Route path="events" element={<Events />} />
          <Route path="map" element={<MapPage />} />
          <Route path="debug" element={<Debug />} />
        </Route>
      </Routes>
    </HashRouter>
  </React.StrictMode>
)
