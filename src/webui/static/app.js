// DOM Elements
const connectionStatus = document.getElementById('connection-status');
const midiStatus = document.getElementById('midi-status');
const midiModel = document.getElementById('midi-model');
const mixerContainer = document.getElementById('mixer-container');
const serverUrl = document.getElementById('server-url');
const statusMessage = document.getElementById('status-message');

// WebSocket Connection
let socket = null;
let reconnectAttempts = 0;
const MAX_RECONNECT_ATTEMPTS = 5;
const RECONNECT_INTERVAL = 3000; // 3 seconds

// Connect to WebSocket server
function connectWebSocket() {
    // Determine WebSocket URL (same host, different protocol)
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/ws`;
    
    connectionStatus.textContent = 'Connecting...';
    connectionStatus.className = 'connecting';
    statusMessage.textContent = `Connecting to server...`;
    
    // Create WebSocket connection
    socket = new WebSocket(wsUrl);
    
    // Connection opened
    socket.addEventListener('open', (event) => {
        connectionStatus.textContent = 'Connected';
        connectionStatus.className = 'connected';
        serverUrl.textContent = wsUrl;
        statusMessage.textContent = 'Connected to server';
        reconnectAttempts = 0;
        console.log('Connected to WebSocket server');
    });
    
    // Listen for messages
    socket.addEventListener('message', (event) => {
        console.log('Message from server:', event.data);
        try {
            const data = JSON.parse(event.data);
            handleServerMessage(data);
        } catch (e) {
            console.error('Error parsing message:', e);
        }
    });
    
    // Connection closed
    socket.addEventListener('close', (event) => {
        connectionStatus.textContent = 'Disconnected';
        connectionStatus.className = 'disconnected';
        statusMessage.textContent = 'Connection lost. Attempting to reconnect...';
        console.log('Disconnected from WebSocket server');
        
        // Attempt to reconnect
        if (reconnectAttempts < MAX_RECONNECT_ATTEMPTS) {
            reconnectAttempts++;
            setTimeout(connectWebSocket, RECONNECT_INTERVAL);
        } else {
            statusMessage.textContent = 'Failed to connect after multiple attempts. Please refresh the page.';
        }
    });
    
    // Connection error
    socket.addEventListener('error', (event) => {
        console.error('WebSocket error:', event);
        statusMessage.textContent = 'Connection error';
    });
}

// Handle different types of server messages
function handleServerMessage(data) {
    switch (data.type) {
        case 'welcome':
            statusMessage.textContent = data.message;
            // Request initial state
            sendMessage({ type: 'getState' });
            break;
            
        case 'midiDeviceUpdate':
            updateMidiDeviceInfo(data.device);
            break;
            
        case 'audioSourcesUpdate':
            updateAudioSources(data.sources);
            break;
            
        default:
            console.log('Unknown message type:', data.type);
    }
}

// Update MIDI device information display
function updateMidiDeviceInfo(device) {
    if (device && device.connected) {
        midiStatus.textContent = 'Connected';
        midiModel.textContent = device.name || 'Unknown';
    } else {
        midiStatus.textContent = 'Not detected';
        midiModel.textContent = '-';
    }
}

// Update audio sources display
function updateAudioSources(sources) {
    if (!sources || sources.length === 0) {
        mixerContainer.innerHTML = '<p>No audio sources available</p>';
        return;
    }
    
    mixerContainer.innerHTML = '';
    
    sources.forEach(source => {
        const channelDiv = document.createElement('div');
        channelDiv.className = 'mixer-channel';
        
        const label = document.createElement('label');
        label.textContent = source.name;
        label.title = source.name; // For tooltip on hover
        
        const slider = document.createElement('input');
        slider.type = 'range';
        slider.min = 0;
        slider.max = 100;
        slider.value = source.volume || 0;
        slider.setAttribute('data-id', source.id);
        
        // Add event listener for volume change
        slider.addEventListener('input', (e) => {
            const value = parseInt(e.target.value);
            sendMessage({
                type: 'setVolume',
                sourceId: source.id,
                volume: value
            });
        });
        
        channelDiv.appendChild(label);
        channelDiv.appendChild(slider);
        mixerContainer.appendChild(channelDiv);
    });
}

// Send message to server
function sendMessage(data) {
    if (socket && socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify(data));
    } else {
        console.warn('Cannot send message, socket not connected');
    }
}

// Initialize the application
function init() {
    console.log('Initializing PulseKontrol WebUI');
    connectWebSocket();
    
    // For demonstration purposes only - remove in production
    setTimeout(() => {
        // Simulate receiving MIDI device update
        handleServerMessage({
            type: 'midiDeviceUpdate',
            device: {
                connected: true,
                name: 'KORG nanoKONTROL2'
            }
        });
        
        // Simulate receiving audio sources
        handleServerMessage({
            type: 'audioSourcesUpdate',
            sources: [
                { id: 'app1', name: 'Firefox', volume: 75 },
                { id: 'app2', name: 'Spotify', volume: 80 },
                { id: 'app3', name: 'Discord', volume: 65 },
                { id: 'app4', name: 'System Sounds', volume: 50 }
            ]
        });
    }, 2000);
}

// Start the application when the page is loaded
window.addEventListener('DOMContentLoaded', init);