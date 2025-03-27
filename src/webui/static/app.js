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
            // Update both sources and assignments if provided
            if (data.sliderAssignments && data.knobAssignments) {
                appState.sliderAssignments = data.sliderAssignments;
                appState.knobAssignments = data.knobAssignments;
            }
            
            // Update control values if provided
            if (data.sliderValues) {
                Object.keys(data.sliderValues).forEach(id => {
                    const slider = appState.sliderControls.find(c => c.id === id);
                    if (slider) {
                        slider.value = data.sliderValues[id];
                    }
                });
            }
            
            if (data.knobValues) {
                Object.keys(data.knobValues).forEach(id => {
                    const knob = appState.knobControls.find(c => c.id === id);
                    if (knob) {
                        knob.value = data.knobValues[id];
                    }
                });
            }
            
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
    sliderAssignments: {}, // Control ID -> Array of Source IDs
    knobAssignments: {},   // Control ID -> Array of Source IDs
    sliderControls: [
        { id: "slider1", value: 50 },
        { id: "slider2", value: 50 },
        { id: "slider3", value: 50 },
        { id: "slider4", value: 50 },
        { id: "slider5", value: 50 },
        { id: "slider6", value: 50 },
        { id: "slider7", value: 50 },
        { id: "slider8", value: 50 },
    ],
    knobControls: [
        { id: "knob1", value: 50 },
        { id: "knob2", value: 50 },
        { id: "knob3", value: 50 },
        { id: "knob4", value: 50 },
        { id: "knob5", value: 50 },
        { id: "knob6", value: 50 },
        { id: "knob7", value: 50 },
        { id: "knob8", value: 50 },
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
    
    // Get all assigned source IDs (flattened from arrays)
    const allAssignedIds = [
        ...Object.values(appState.sliderAssignments).flat(),
        ...Object.values(appState.knobAssignments).flat()
    ];
    
    // Render slider controls with assignments
    appState.sliderControls.forEach(control => {
        const assignedSourceIds = appState.sliderAssignments[control.id] || [];
        
        // Get the sources that exist in the current available sources
        const availableSources = assignedSourceIds
            .map(id => appState.audioSources.find(s => s.id === id))
            .filter(s => s); // Filter out undefined values
            
        const controlDiv = document.createElement('div');
        controlDiv.className = 'mixer-channel slider-control';
        controlDiv.id = control.id;
        controlDiv.setAttribute('data-control-type', 'slider');
        controlDiv.setAttribute('draggable', 'false');
        
        if (assignedSourceIds.length > 0) {
            // Show assigned sources, including ones that might be unavailable
            renderControlWithSources(controlDiv, control, assignedSourceIds, availableSources);
        } else {
            // Show empty control
            renderEmptyControl(controlDiv, control);
        }
        
        slidersContainer.appendChild(controlDiv);
    });
    
    // Render knob controls with assignments
    appState.knobControls.forEach(control => {
        const assignedSourceIds = appState.knobAssignments[control.id] || [];
        
        // Get the sources that exist in the current available sources
        const availableSources = assignedSourceIds
            .map(id => appState.audioSources.find(s => s.id === id))
            .filter(s => s); // Filter out undefined values
            
        const controlDiv = document.createElement('div');
        controlDiv.className = 'mixer-channel knob-control';
        controlDiv.id = control.id;
        controlDiv.setAttribute('data-control-type', 'knob');
        controlDiv.setAttribute('draggable', 'false');
        
        if (assignedSourceIds.length > 0) {
            // Show assigned sources, including ones that might be unavailable
            renderControlWithSources(controlDiv, control, assignedSourceIds, availableSources);
        } else {
            // Show empty control
            renderEmptyControl(controlDiv, control);
        }
        
        knobsContainer.appendChild(controlDiv);
    });
    
    // Only unassigned sources go in the right column
    const unassignedSources = appState.audioSources.filter(
        source => !allAssignedIds.includes(source.id)
    );
    
    // Render unassigned audio sources
    if (unassignedSources.length === 0) {
        sourcesContainer.innerHTML = '<div class="control-placeholder">No unassigned sources available</div>';
    } else {
        unassignedSources.forEach(source => {
            const sourceDiv = document.createElement('div');
            sourceDiv.className = 'mixer-channel source';
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
            
            // Add drag event handlers
            sourceDiv.addEventListener('dragstart', handleDragStart);
            sourceDiv.addEventListener('dragend', handleDragEnd);
            
            sourcesContainer.appendChild(sourceDiv);
        });
    }
    
    // Add drop event handlers to control containers
    setupDropZones();
}

function renderControlWithSources(controlDiv, control, assignedSourceIds, availableSources) {
    // Add control visualization based on type
    if (controlDiv.getAttribute('data-control-type') === 'slider') {
        renderSliderVisualization(controlDiv, control);
    } else {
        renderKnobVisualization(controlDiv, control);
    }
    
    // Add control number inline with visual
    const controlVisual = controlDiv.querySelector('.control-visual');
    const controlNumber = document.createElement('div');
    controlNumber.textContent = control.id.replace('slider', '').replace('knob', '');
    controlNumber.className = 'control-number';
    controlVisual.insertBefore(controlNumber, controlVisual.firstChild);
    
    // Add sources list - also a drop zone
    const sourcesList = document.createElement('div');
    sourcesList.className = 'sources-list';
    sourcesList.setAttribute('data-parent-control', control.id);
    sourcesList.setAttribute('data-parent-type', controlDiv.getAttribute('data-control-type'));
    
    // Make the sources list a drop target too
    sourcesList.addEventListener('dragover', handleDragOver);
    sourcesList.addEventListener('dragenter', handleDragEnter);
    sourcesList.addEventListener('dragleave', handleDragLeave);
    sourcesList.addEventListener('drop', handleDrop);
    
    // Render available sources first (those that exist in current audio sources)
    availableSources.forEach(source => {
        const sourceItem = document.createElement('div');
        sourceItem.className = 'source-item';
        sourceItem.setAttribute('draggable', 'true');
        sourceItem.setAttribute('data-source-id', source.id);
        sourceItem.setAttribute('data-parent-control', control.id);
        sourceItem.setAttribute('data-parent-type', controlDiv.getAttribute('data-control-type'));
        
        // Add drag event handlers
        sourceItem.addEventListener('dragstart', handleDragStart);
        sourceItem.addEventListener('dragend', handleDragEnd);
        
        // Add type badge
        const typeBadge = document.createElement('span');
        typeBadge.className = 'type-badge';
        typeBadge.textContent = source.type;
        sourceItem.appendChild(typeBadge);
        
        // Add source name
        const sourceName = document.createElement('span');
        sourceName.textContent = source.name;
        sourceName.title = source.name; // For tooltip on hover
        sourceItem.appendChild(sourceName);
        
        sourcesList.appendChild(sourceItem);
    });
    
    // Find missing sources (those assigned but not currently available)
    const availableIds = availableSources.map(s => s.id);
    const missingSourceIds = assignedSourceIds.filter(id => !availableIds.includes(id));
    
    // Construct missing source objects based on what we can get from config
    missingSourceIds.forEach(sourceId => {
        // Look in the server's assignments to get information about this source
        // We need to create a placeholder with at least a name
        let sourceType = "unknown";
        let sourceName = sourceId; // Fallback to showing ID if we can't find a name
        
        // Try to extract type and name from the source ID
        // Format is often something like "playback:Chromium"
        if (sourceId.includes(':')) {
            const parts = sourceId.split(':');
            sourceType = parts[0];
            sourceName = parts.slice(1).join(':');
        }
        
        const sourceItem = document.createElement('div');
        sourceItem.className = 'source-item missing-source';
        // Still draggable but visually different
        sourceItem.setAttribute('draggable', 'true');
        sourceItem.setAttribute('data-source-id', sourceId);
        sourceItem.setAttribute('data-parent-control', control.id);
        sourceItem.setAttribute('data-parent-type', controlDiv.getAttribute('data-control-type'));
        
        // Add drag event handlers
        sourceItem.addEventListener('dragstart', handleDragStart);
        sourceItem.addEventListener('dragend', handleDragEnd);
        
        // Add type badge
        const typeBadge = document.createElement('span');
        typeBadge.className = 'type-badge';
        typeBadge.textContent = sourceType;
        sourceItem.appendChild(typeBadge);
        
        // Add source name
        const sourceNameElement = document.createElement('span');
        sourceNameElement.textContent = sourceName;
        sourceNameElement.title = sourceName; // For tooltip on hover
        sourceItem.appendChild(sourceNameElement);
        
        // Add missing indicator
        const missingIndicator = document.createElement('span');
        missingIndicator.className = 'missing-indicator';
        missingIndicator.textContent = ' (not available)';
        sourceItem.appendChild(missingIndicator);
        
        sourcesList.appendChild(sourceItem);
    });
    
    controlDiv.appendChild(sourcesList);
}

function renderSliderVisualization(controlDiv, control) {
    const controlVisual = document.createElement('div');
    controlVisual.className = 'control-visual';
    
    // Create progress track
    const progressTrack = document.createElement('div');
    progressTrack.className = 'progress-track';
    
    // Create progress fill
    const progressFill = document.createElement('div');
    progressFill.className = 'progress-fill';
    progressFill.style.width = `${control.value}%`;
    
    // Create value label
    const valueLabel = document.createElement('span');
    valueLabel.className = 'value-label';
    valueLabel.textContent = control.value;
    
    // NOTE: The sliders are read-only and show the levels set by the MIDI device
    // We don't create range inputs since they should not be adjustable from the web GUI
    
    // Assemble components
    progressTrack.appendChild(progressFill);
    controlVisual.appendChild(progressTrack);
    controlVisual.appendChild(valueLabel);
    
    controlDiv.appendChild(controlVisual);
}

// NOTE: We've removed the input handlers for the slider controls since they should be read-only 
// and only controlled by the MIDI device

// Use the same visualization for knobs
function renderKnobVisualization(controlDiv, control) {
    // Use same horizontal slider for both controls
    renderSliderVisualization(controlDiv, control);
}

function renderEmptyControl(controlDiv, control) {
    // Add control visualization based on type
    if (controlDiv.getAttribute('data-control-type') === 'slider') {
        renderSliderVisualization(controlDiv, control);
    } else {
        renderKnobVisualization(controlDiv, control);
    }
    
    // Add control number inline with visual
    const controlVisual = controlDiv.querySelector('.control-visual');
    const controlNumber = document.createElement('div');
    controlNumber.textContent = control.id.replace('slider', '').replace('knob', '');
    controlNumber.className = 'control-number';
    controlVisual.insertBefore(controlNumber, controlVisual.firstChild);
    
    // Create content for empty control
    const placeholder = document.createElement('div');
    placeholder.className = 'control-placeholder';
    placeholder.textContent = `Drop audio source here`;
    controlDiv.appendChild(placeholder);
}

// Drag and drop functionality
let draggedItem = null;

function handleDragStart(e) {
    this.classList.add('dragging');
    draggedItem = this;
    e.dataTransfer.effectAllowed = 'move';
    
    // Store source ID and parent info (if from a control)
    e.dataTransfer.setData('source-id', this.getAttribute('data-source-id'));
    
    // If dragging from a control, store the parent info
    const parentControl = this.getAttribute('data-parent-control');
    const parentType = this.getAttribute('data-parent-type');
    
    if (parentControl && parentType) {
        e.dataTransfer.setData('parent-control', parentControl);
        e.dataTransfer.setData('parent-type', parentType);
    }
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
    
    // Make sources container a drop zone for unassigning
    sourcesContainer.addEventListener('dragover', handleDragOver);
    sourcesContainer.addEventListener('dragenter', handleDragEnter);
    sourcesContainer.addEventListener('dragleave', handleDragLeave);
    sourcesContainer.addEventListener('drop', handleSourcesDrop);
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
    
    const sourceId = e.dataTransfer.getData('source-id');
    const controlId = this.id;
    const controlType = this.getAttribute('data-control-type');
    
    // Check if we're coming from another control
    const oldParentControl = e.dataTransfer.getData('parent-control');
    const oldParentType = e.dataTransfer.getData('parent-type');
    
    if (oldParentControl && oldParentType) {
        // Remove from old control first
        unassignSource(oldParentControl, sourceId, oldParentType);
    }
    
    // Assign source to the new control
    assignSource(controlId, sourceId, controlType);
    
    return false;
}

function handleSourcesDrop(e) {
    e.preventDefault();
    
    // Remove drop target highlighting
    this.classList.remove('drop-target');
    
    if (!draggedItem) return;
    
    const sourceId = e.dataTransfer.getData('source-id');
    
    // Check if we're coming from a control
    const oldParentControl = e.dataTransfer.getData('parent-control');
    const oldParentType = e.dataTransfer.getData('parent-type');
    
    if (oldParentControl && oldParentType) {
        // Unassign from the control
        unassignSource(oldParentControl, sourceId, oldParentType);
    }
    
    return false;
}

function assignSource(controlId, sourceId, controlType) {
    // Initialize the array if it doesn't exist
    if (controlType === 'slider') {
        if (!appState.sliderAssignments[controlId]) {
            appState.sliderAssignments[controlId] = [];
        }
        
        // Only add if not already assigned to this control
        if (!appState.sliderAssignments[controlId].includes(sourceId)) {
            appState.sliderAssignments[controlId].push(sourceId);
        }
    } else if (controlType === 'knob') {
        if (!appState.knobAssignments[controlId]) {
            appState.knobAssignments[controlId] = [];
        }
        
        // Only add if not already assigned to this control
        if (!appState.knobAssignments[controlId].includes(sourceId)) {
            appState.knobAssignments[controlId].push(sourceId);
        }
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

function unassignSource(controlId, sourceId, controlType) {
    // Remove the source ID from the appropriate assignment array
    if (controlType === 'slider') {
        if (appState.sliderAssignments[controlId]) {
            appState.sliderAssignments[controlId] = appState.sliderAssignments[controlId]
                .filter(id => id !== sourceId);
            
            // Clean up empty arrays
            if (appState.sliderAssignments[controlId].length === 0) {
                delete appState.sliderAssignments[controlId];
            }
        }
    } else if (controlType === 'knob') {
        if (appState.knobAssignments[controlId]) {
            appState.knobAssignments[controlId] = appState.knobAssignments[controlId]
                .filter(id => id !== sourceId);
            
            // Clean up empty arrays
            if (appState.knobAssignments[controlId].length === 0) {
                delete appState.knobAssignments[controlId];
            }
        }
    }
    
    // Send unassignment to server
    sendMessage({
        type: 'unassignControl',
        controlId: controlId,
        controlType: controlType,
        sourceId: sourceId
    });
    
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