// DOM Elements
const connectionStatus = document.getElementById('connection-status');
const midiStatus = document.getElementById('midi-status');
const midiModel = document.getElementById('midi-model');
const slidersContainer = document.getElementById('sliders-container');
const knobsContainer = document.getElementById('knobs-container');
const sourcesContainer = document.getElementById('sources-container');
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

// Application state
const appState = {
    audioSources: [],
    sliderAssignments: {}, // Control ID -> Source ID
    knobAssignments: {},   // Control ID -> Source ID
    sliderControls: [
        { id: "slider1", name: "Slider 1" },
        { id: "slider2", name: "Slider 2" },
        { id: "slider3", name: "Slider 3" },
        { id: "slider4", name: "Slider 4" },
        { id: "slider5", name: "Slider 5" },
        { id: "slider6", name: "Slider 6" },
        { id: "slider7", name: "Slider 7" },
        { id: "slider8", name: "Slider 8" },
    ],
    knobControls: [
        { id: "knob1", name: "Knob 1" },
        { id: "knob2", name: "Knob 2" },
        { id: "knob3", name: "Knob 3" },
        { id: "knob4", name: "Knob 4" },
        { id: "knob5", name: "Knob 5" },
        { id: "knob6", name: "Knob 6" },
        { id: "knob7", name: "Knob 7" },
        { id: "knob8", name: "Knob 8" },
    ]
};

// Update audio sources display
function updateAudioSources(sources) {
    if (!sources || sources.length === 0) {
        sourcesContainer.innerHTML = '<div class="control-placeholder">No audio sources available</div>';
        return;
    }
    
    // Update state
    appState.audioSources = sources;
    
    // Clear all containers before updating
    slidersContainer.innerHTML = '';
    knobsContainer.innerHTML = '';
    sourcesContainer.innerHTML = '';
    
    // Render slider controls with assignments
    appState.sliderControls.forEach(control => {
        const assignedSourceId = appState.sliderAssignments[control.id];
        const assignedSource = assignedSourceId ? 
            appState.audioSources.find(s => s.id === assignedSourceId) : null;
            
        const controlDiv = document.createElement('div');
        controlDiv.className = 'mixer-channel slider-control';
        controlDiv.id = control.id;
        controlDiv.setAttribute('data-control-type', 'slider');
        controlDiv.setAttribute('draggable', 'false');
        
        if (assignedSource) {
            // Show assigned source
            renderAssignedSource(controlDiv, control, assignedSource);
        } else {
            // Show empty control
            renderEmptyControl(controlDiv, control);
        }
        
        slidersContainer.appendChild(controlDiv);
    });
    
    // Render knob controls with assignments
    appState.knobControls.forEach(control => {
        const assignedSourceId = appState.knobAssignments[control.id];
        const assignedSource = assignedSourceId ? 
            appState.audioSources.find(s => s.id === assignedSourceId) : null;
            
        const controlDiv = document.createElement('div');
        controlDiv.className = 'mixer-channel knob-control';
        controlDiv.id = control.id;
        controlDiv.setAttribute('data-control-type', 'knob');
        controlDiv.setAttribute('draggable', 'false');
        
        if (assignedSource) {
            // Show assigned source
            renderAssignedSource(controlDiv, control, assignedSource);
        } else {
            // Show empty control
            renderEmptyControl(controlDiv, control);
        }
        
        knobsContainer.appendChild(controlDiv);
    });
    
    // Find unassigned sources
    const assignedSourceIds = [
        ...Object.values(appState.sliderAssignments),
        ...Object.values(appState.knobAssignments)
    ];
    
    const unassignedSources = appState.audioSources.filter(
        source => !assignedSourceIds.includes(source.id)
    );
    
    // Render unassigned sources
    if (unassignedSources.length === 0) {
        sourcesContainer.innerHTML = '<div class="control-placeholder">All sources are assigned</div>';
    } else {
        unassignedSources.forEach(source => {
            const sourceDiv = document.createElement('div');
            sourceDiv.className = 'mixer-channel unassigned';
            sourceDiv.id = `source-${source.id}`;
            sourceDiv.setAttribute('data-source-id', source.id);
            sourceDiv.setAttribute('draggable', 'true');
            
            // Add type badge
            const typeBadge = document.createElement('span');
            typeBadge.className = 'type-badge';
            typeBadge.textContent = source.type;
            sourceDiv.appendChild(typeBadge);
            
            // Add label
            const label = document.createElement('label');
            label.textContent = source.name;
            label.title = source.name; // For tooltip on hover
            sourceDiv.appendChild(label);
            
            // Add slider for volume control
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
            
            sourceDiv.appendChild(slider);
            
            // Add drag event handlers
            sourceDiv.addEventListener('dragstart', handleDragStart);
            sourceDiv.addEventListener('dragend', handleDragEnd);
            
            sourcesContainer.appendChild(sourceDiv);
        });
    }
    
    // Add drop event handlers to control containers
    setupDropZones();
}

function renderAssignedSource(controlDiv, control, source) {
    // Add control name
    const controlName = document.createElement('small');
    controlName.textContent = `${control.name}: ${source.name}`;
    controlName.className = 'control-name';
    controlDiv.appendChild(controlName);
    
    // Add type badge
    const typeBadge = document.createElement('span');
    typeBadge.className = 'type-badge';
    typeBadge.textContent = source.type;
    controlDiv.appendChild(typeBadge);
    
    // Add label
    const label = document.createElement('label');
    label.textContent = source.name;
    label.title = source.name; // For tooltip on hover
    controlDiv.appendChild(label);
    
    // Add slider for volume control
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
    
    controlDiv.appendChild(slider);
    
    // Add remove button
    const removeBtn = document.createElement('button');
    removeBtn.textContent = 'Remove';
    removeBtn.className = 'remove-btn';
    removeBtn.addEventListener('click', () => unassignSource(control.id, source.id));
    controlDiv.appendChild(removeBtn);
    
    // Store source ID in data attribute
    controlDiv.setAttribute('data-source-id', source.id);
}

function renderEmptyControl(controlDiv, control) {
    // Create content for empty control
    const placeholder = document.createElement('div');
    placeholder.className = 'control-placeholder';
    placeholder.textContent = `Drop audio source here for ${control.name}`;
    controlDiv.appendChild(placeholder);
}

// Drag and drop functionality
let draggedItem = null;

function handleDragStart(e) {
    this.classList.add('dragging');
    draggedItem = this;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', this.getAttribute('data-source-id'));
}

function handleDragEnd(e) {
    this.classList.remove('dragging');
    
    // Remove drop target highlighting from all containers
    document.querySelectorAll('.drop-target').forEach(item => {
        item.classList.remove('drop-target');
    });
}

function setupDropZones() {
    // Set up drop targets (sliders)
    const sliderControls = document.querySelectorAll('.slider-control');
    sliderControls.forEach(control => {
        control.addEventListener('dragover', handleDragOver);
        control.addEventListener('dragenter', handleDragEnter);
        control.addEventListener('dragleave', handleDragLeave);
        control.addEventListener('drop', handleDrop);
    });
    
    // Set up drop targets (knobs)
    const knobControls = document.querySelectorAll('.knob-control');
    knobControls.forEach(control => {
        control.addEventListener('dragover', handleDragOver);
        control.addEventListener('dragenter', handleDragEnter);
        control.addEventListener('dragleave', handleDragLeave);
        control.addEventListener('drop', handleDrop);
    });
}

function handleDragOver(e) {
    e.preventDefault();
    return false;
}

function handleDragEnter(e) {
    this.classList.add('drop-target');
}

function handleDragLeave(e) {
    this.classList.remove('drop-target');
}

function handleDrop(e) {
    e.preventDefault();
    
    // Remove drop target highlighting
    this.classList.remove('drop-target');
    
    if (!draggedItem) return;
    
    const sourceId = e.dataTransfer.getData('text/plain');
    const controlId = this.id;
    const controlType = this.getAttribute('data-control-type');
    
    // Assign source to control
    assignSource(controlId, sourceId, controlType);
    
    return false;
}

function assignSource(controlId, sourceId, controlType) {
    // Check if source is already assigned elsewhere and remove it
    for (const sliderId in appState.sliderAssignments) {
        if (appState.sliderAssignments[sliderId] === sourceId) {
            delete appState.sliderAssignments[sliderId];
        }
    }
    
    for (const knobId in appState.knobAssignments) {
        if (appState.knobAssignments[knobId] === sourceId) {
            delete appState.knobAssignments[knobId];
        }
    }
    
    // Assign to the new control
    if (controlType === 'slider') {
        appState.sliderAssignments[controlId] = sourceId;
    } else if (controlType === 'knob') {
        appState.knobAssignments[controlId] = sourceId;
    }
    
    // Send assignment to server
    sendMessage({
        type: 'assignControl',
        controlId: controlId,
        controlType: controlType,
        sourceId: sourceId
    });
    
    // Update UI to reflect new assignments
    updateAudioSources(appState.audioSources);
}

function unassignSource(controlId, sourceId) {
    // Find the assignment type
    let controlType = null;
    
    if (controlId in appState.sliderAssignments) {
        delete appState.sliderAssignments[controlId];
        controlType = 'slider';
    } else if (controlId in appState.knobAssignments) {
        delete appState.knobAssignments[controlId];
        controlType = 'knob';
    }
    
    // Send unassignment to server
    if (controlType) {
        sendMessage({
            type: 'unassignControl',
            controlId: controlId,
            controlType: controlType,
            sourceId: sourceId
        });
    }
    
    // Update UI to reflect new assignments
    updateAudioSources(appState.audioSources);
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
    
    // For demonstration purposes only - MIDI device simulation
    setTimeout(() => {
        // Simulate receiving MIDI device update
        handleServerMessage({
            type: 'midiDeviceUpdate',
            device: {
                connected: true,
                name: 'KORG nanoKONTROL2'
            }
        });
    }, 2000);
}

// Start the application when the page is loaded
window.addEventListener('DOMContentLoaded', init);